//go:build server

package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrLicenseInvalid 表示授权码格式、签名、签发方、产品或能力清单无效。
	ErrLicenseInvalid = errors.New("license code invalid")
	// ErrLicenseInstanceMismatch 表示授权码绑定的是其他实例。
	ErrLicenseInstanceMismatch = errors.New("license code belongs to another instance")
	// ErrLicenseExpired 表示授权码已过授权期限。
	ErrLicenseExpired = errors.New("license code expired")
	// ErrLicenseSuperseded 表示授权码的签发时间不晚于当前授权。
	ErrLicenseSuperseded = errors.New("license code is not newer than the current license")
)

// License 定义实例标识与授权状态；Capabilities 是授权码授予的能力，授权到期后实例按免费取值运行。
type License struct {
	InstanceID   string
	Status       domain.LicenseStatus
	LicenseID    string
	Customer     string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	Capabilities domain.InstanceCapabilities
}

// Capabilities 返回实例当前生效的能力；未激活授权或授权到期时按免费取值执行。
func Capabilities(ctx context.Context, db bun.IDB) (domain.InstanceCapabilities, error) {
	current, err := loadLicense(ctx, db)
	if err != nil {
		return domain.InstanceCapabilities{}, err
	}
	if current.Status != domain.LicenseStatusActive {
		return domain.FreeInstanceCapabilities(), nil
	}
	return current.Capabilities, nil
}

// CustomBrandingUntil 返回部署品牌配置可以生效到的时间；授权未授予自定义品牌时返回零值。
func CustomBrandingUntil(ctx context.Context, db bun.IDB) (time.Time, error) {
	current, err := loadLicense(ctx, db)
	if err != nil {
		return time.Time{}, err
	}
	return customBrandingUntil(current), nil
}

// loadLicense 读取当前实例的授权并按当前时间判断状态。
func loadLicense(ctx context.Context, db bun.IDB) (License, error) {
	record := &servermodels.InstanceLicense{}
	err := db.NewSelect().Model(record).
		Where("il.instance_id = (SELECT dep.instance_id FROM deployments AS dep LIMIT 1)").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return License{Status: domain.LicenseStatusNone, Capabilities: domain.FreeInstanceCapabilities()}, nil
	}
	if err != nil {
		return License{}, fmt.Errorf("load instance license: %w", err)
	}
	return licenseFromModel(record, time.Now())
}

// licenseFromModel 把授权行转换为授权状态，按 now 判断是否到期。
func licenseFromModel(record *servermodels.InstanceLicense, now time.Time) (License, error) {
	var granted map[string]json.RawMessage
	if err := json.Unmarshal(record.Capabilities, &granted); err != nil {
		return License{}, fmt.Errorf("decode instance license capabilities: %w", err)
	}
	status := domain.LicenseStatusActive
	if !now.Before(record.ExpiresAt) {
		status = domain.LicenseStatusExpired
	}
	return License{
		InstanceID: record.InstanceID, Status: status, LicenseID: record.LicenseID, Customer: record.Customer,
		IssuedAt: record.IssuedAt, ExpiresAt: record.ExpiresAt,
		Capabilities: capabilitiesFromLicense(granted),
	}, nil
}

// capabilitiesFromLicense 在免费取值上应用授权码列出的能力键。
func capabilitiesFromLicense(granted map[string]json.RawMessage) domain.InstanceCapabilities {
	capabilities := domain.FreeInstanceCapabilities()
	if raw, ok := granted[license.CapabilityWorkspaceLimit]; ok {
		if limit, err := license.WorkspaceLimit(raw); err == nil {
			capabilities.WorkspaceLimit = limit
		}
	}
	if raw, ok := granted[license.CapabilityCustomBranding]; ok {
		if custom, err := license.CustomBranding(raw); err == nil {
			capabilities.CustomBranding = custom
		}
	}
	return capabilities
}

// LicenseQuery 读取实例授权状态。
type LicenseQuery struct {
	db *bun.DB
}

// NewLicenseQuery 创建实例授权查询。
func NewLicenseQuery(db *bun.DB) *LicenseQuery {
	return &LicenseQuery{db: db}
}

// Execute 返回实例标识与当前的授权状态。
func (q *LicenseQuery) Execute(ctx context.Context) (License, error) {
	deployment, err := Load(ctx, q.db)
	if err != nil {
		return License{}, err
	}
	current, err := loadLicense(ctx, q.db)
	current.InstanceID = deployment.InstanceID
	return current, err
}

// ActivateLicenseAction 用授权码激活或替换实例授权。
type ActivateLicenseAction struct {
	db   *bun.DB
	keys license.Keys
}

// NewActivateLicenseAction 创建实例授权激活操作，keys 是用于验签的 control 签名公钥。
func NewActivateLicenseAction(db *bun.DB, keys license.Keys) *ActivateLicenseAction {
	return &ActivateLicenseAction{db: db, keys: keys}
}

// Execute 验签授权码后保存为实例授权，签发时间不晚于当前授权的其他授权码返回 ErrLicenseSuperseded。
func (a *ActivateLicenseAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, code string) (License, error) {
	claims, err := license.Parse(code, a.keys)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrLicenseInvalid, err)
	}
	return storeLicense(ctx, a.db, operator, claims)
}

// storeLicense 在事务内校验实例标识与授权期限后保存授权：首次激活或签发时间晚于当前授权时写入，与当前授权码相同时原样返回，其余返回 ErrLicenseSuperseded；operator 非空时先确认其仍是有效部署管理员；完成后按新授权应用部署品牌。
func storeLicense(ctx context.Context, db *bun.DB, operator *servermodels.AccountIdentity, claims license.Claims) (License, error) {
	capabilities, err := json.Marshal(claims.Capabilities)
	if err != nil {
		return License{}, err
	}
	var output License
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if operator != nil {
			if err := lockActiveAdmins(ctx, tx, operator); err != nil {
				return err
			}
		}
		deployment, err := Lock(ctx, tx)
		if err != nil {
			return err
		}
		if claims.InstanceID != deployment.InstanceID {
			return ErrLicenseInstanceMismatch
		}
		now := time.Now()
		if !now.Before(claims.ExpiresAt) {
			return ErrLicenseExpired
		}
		record := &servermodels.InstanceLicense{InstanceID: deployment.InstanceID}
		err = tx.NewSelect().Model(record).WherePK().Scan(ctx)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// 实例首次激活授权。
		case err != nil:
			return err
		case record.LicenseCode == claims.Code:
			output, err = licenseFromModel(record, now)
			return err
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
			Column("instance_id", "license_id", "customer", "license_code", "capabilities", "issued_at", "expires_at").
			On("CONFLICT (instance_id) DO UPDATE").
			Set("license_id = EXCLUDED.license_id").
			Set("customer = EXCLUDED.customer").
			Set("license_code = EXCLUDED.license_code").
			Set("capabilities = EXCLUDED.capabilities").
			Set("issued_at = EXCLUDED.issued_at").
			Set("expires_at = EXCLUDED.expires_at").
			Set("updated_at = now()").
			Returning("created_at, updated_at").
			Exec(ctx); err != nil {
			return err
		}
		output, err = licenseFromModel(record, now)
		return err
	})
	if err != nil {
		return License{}, err
	}
	brand.EnableOverrideUntil(customBrandingUntil(output))
	return output, nil
}

// customBrandingUntil 返回授权允许部署品牌配置生效到的时间，未授予时返回零值。
func customBrandingUntil(current License) time.Time {
	if current.Capabilities.CustomBranding {
		return current.ExpiresAt
	}
	return time.Time{}
}
