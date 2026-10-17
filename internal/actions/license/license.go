//go:build server

// Package license 实现部署登记、授权激活与生效状态读取。
package license

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	licensecode "github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrLicenseInvalid 表示授权码格式、签名、签发方或产品无效。
	ErrLicenseInvalid = errors.New("license code invalid")
	// ErrLicenseDeploymentMismatch 表示授权码绑定的是其他部署。
	ErrLicenseDeploymentMismatch = errors.New("license code belongs to another deployment")
	// ErrLicenseExpired 表示授权码已过授权期限。
	ErrLicenseExpired = errors.New("license code expired")
	// ErrLicenseSuperseded 表示授权码的签发时间不晚于当前授权。
	ErrLicenseSuperseded = errors.New("license code is not newer than the current license")
)

// License 定义部署身份与授权状态；有效授权即产品功能全开。
type License struct {
	DeploymentID string
	Status       domain.LicenseStatus
	LicenseID    string
	Customer     string
	IssuedAt     time.Time
	ExpiresAt    time.Time
}

// Licensed 返回授权是否处于有效期内。
func (l License) Licensed() bool {
	return l.Status.Licensed()
}

// IsLicensed 读取平台当前是否处于有效授权。
func IsLicensed(ctx context.Context, db bun.IDB) (bool, error) {
	current, err := loadLicense(ctx, db)
	if err != nil {
		return false, err
	}
	return current.Licensed(), nil
}

// EffectiveCreationQuota 返回账号自建工作区额度；未授权时恒为 0。
func EffectiveCreationQuota(ctx context.Context, db bun.IDB, platformQuota int) (int, error) {
	licensed, err := IsLicensed(ctx, db)
	if err != nil {
		return 0, err
	}
	if !licensed || platformQuota < 0 {
		return 0, nil
	}
	return platformQuota, nil
}

// loadLicense 读取平台当前的授权，是否到期按数据库时刻判断。
func loadLicense(ctx context.Context, db bun.IDB) (License, error) {
	var record struct {
		servermodels.License `bun:",extend"`
		Active               bool `bun:"active"`
	}
	err := db.NewSelect().Model(&record).
		ColumnExpr("lic.*").
		ColumnExpr("lic.expires_at > now() AS active").
		Where("lic.deployment_id = (SELECT pf.deployment_id FROM platforms AS pf LIMIT 1)").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return License{Status: domain.LicenseStatusNone}, nil
	}
	if err != nil {
		return License{}, fmt.Errorf("load license: %w", err)
	}
	return licenseFromModel(&record.License, record.Active), nil
}

// licenseFromModel 把授权行转换为授权状态，active 表示授权尚未到期。
func licenseFromModel(record *servermodels.License, active bool) License {
	status := domain.LicenseStatusExpired
	if active {
		status = domain.LicenseStatusActive
	}
	return License{
		DeploymentID: record.DeploymentID, Status: status, LicenseID: record.LicenseID, Customer: record.Customer,
		IssuedAt: record.IssuedAt, ExpiresAt: record.ExpiresAt,
	}
}

// LicenseQuery 读取授权状态。
type LicenseQuery struct {
	db *bun.DB
}

// NewLicenseQuery 创建授权查询。
func NewLicenseQuery(db *bun.DB) *LicenseQuery {
	return &LicenseQuery{db: db}
}

// Execute 返回部署标识、当前的授权状态。
func (q *LicenseQuery) Execute(ctx context.Context) (License, error) {
	platform, err := platformaction.Load(ctx, q.db)
	if err != nil {
		return License{}, err
	}
	current, err := loadLicense(ctx, q.db)
	current.DeploymentID = platform.DeploymentID
	return current, err
}

// ActivateLicenseAction 用授权码激活或替换授权。
type ActivateLicenseAction struct {
	db    *bun.DB
	keys  licensecode.Keys
	state platformaction.DeploymentReloader
}

// NewActivateLicenseAction 创建授权激活操作，keys 是用于验签的 control 签名公钥，state 在保存授权后刷新。
func NewActivateLicenseAction(db *bun.DB, keys licensecode.Keys, state platformaction.DeploymentReloader) *ActivateLicenseAction {
	return &ActivateLicenseAction{db: db, keys: keys, state: state}
}

// Execute 验签授权码后保存为授权，签发时间不晚于当前授权的其他授权码返回 ErrLicenseSuperseded。
func (a *ActivateLicenseAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, code string) (License, error) {
	claims, err := licensecode.Parse(code, a.keys)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrLicenseInvalid, err)
	}
	return storeLicense(ctx, a.db, a.state, operator, claims)
}

// storeLicense 在事务内校验部署标识与授权期限后保存授权。
func storeLicense(ctx context.Context, db *bun.DB, state platformaction.DeploymentReloader, operator *servermodels.AccountIdentity, claims licensecode.Claims) (License, error) {
	// 兼容旧库列：capabilities 存空对象，产品侧不再读取分项能力。
	capabilities, err := json.Marshal(map[string]any{})
	if err != nil {
		return License{}, err
	}
	var output License
	stored := false
	err = serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if operator != nil {
			if err := platformaction.LockActiveAdmins(ctx, tx, operator); err != nil {
				return err
			}
		}
		platform, err := platformaction.Lock(ctx, tx)
		if err != nil {
			return err
		}
		if claims.DeploymentID != platform.DeploymentID {
			return ErrLicenseDeploymentMismatch
		}
		var valid bool
		if err := tx.NewRaw("SELECT ?::timestamptz > now()", claims.ExpiresAt).Scan(ctx, &valid); err != nil {
			return err
		}
		if !valid {
			return ErrLicenseExpired
		}
		record := &servermodels.License{DeploymentID: platform.DeploymentID}
		err = tx.NewSelect().Model(record).WherePK().Scan(ctx)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// 平台首次激活授权。
		case err != nil:
			return err
		case record.LicenseCode == claims.Code:
			output = licenseFromModel(record, true)
			return nil
		case !claims.IssuedAt.After(record.IssuedAt):
			return ErrLicenseSuperseded
		}
		record.LicenseID = claims.LicenseID
		record.Customer = claims.Customer
		record.LicenseCode = claims.Code
		record.Capabilities = capabilities
		record.IssuedAt = claims.IssuedAt
		record.ExpiresAt = claims.ExpiresAt
		if _, err := tx.NewInsert().Model(record).
			Column("deployment_id", "license_id", "customer", "license_code", "capabilities", "issued_at", "expires_at").
			On("CONFLICT (deployment_id) DO UPDATE").
			Set("license_id = EXCLUDED.license_id").
			Set("customer = EXCLUDED.customer").
			Set("license_code = EXCLUDED.license_code").
			Set("capabilities = EXCLUDED.capabilities").
			Set("issued_at = EXCLUDED.issued_at").
			Set("expires_at = EXCLUDED.expires_at").
			Returning("created_at, updated_at").
			Exec(ctx); err != nil {
			return err
		}
		stored = true
		output = licenseFromModel(record, true)
		return nil
	})
	if err != nil {
		return License{}, err
	}
	if stored {
		slog.InfoContext(ctx, "已保存授权", "license_id", claims.LicenseID, "issued_at", claims.IssuedAt, "expires_at", claims.ExpiresAt)
	}
	if err := state.Reload(context.WithoutCancel(ctx)); err != nil {
		slog.WarnContext(ctx, "保存授权后刷新部署状态失败", "error", err)
	}
	return output, nil
}
