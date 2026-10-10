//go:build server

// Package license 实现授权的激活、保存与生效能力读取，以及与 control 的服务器登记、授权同步和运行指标上报。
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
	// ErrLicenseInvalid 表示授权码格式、签名、签发方、产品或能力清单无效。
	ErrLicenseInvalid = errors.New("license code invalid")
	// ErrLicenseServerMismatch 表示授权码绑定的是其他服务器。
	ErrLicenseServerMismatch = errors.New("license code belongs to another server")
	// ErrLicenseExpired 表示授权码已过授权期限。
	ErrLicenseExpired = errors.New("license code expired")
	// ErrLicenseSuperseded 表示授权码的签发时间不晚于当前授权。
	ErrLicenseSuperseded = errors.New("license code is not newer than the current license")
)

// ControlStatus 定义与 control 同步的结果：SyncedAt 为最近一次成功的时间，FailedAt 与 Error 为此后最近一次失败的时间与原因。
type ControlStatus struct {
	SyncedAt *time.Time
	FailedAt *time.Time
	Error    string
}

// License 定义服务器标识与授权状态；Capabilities 是授权码授予的能力，授权到期后平台按免费取值运行；ControlMissingAt 是与 control 同步时查不到本服务器授权的起始时间，Sync 是与 control 的同步结果，只由授权查询填写。
type License struct {
	ServerID         string
	Status           domain.LicenseStatus
	LicenseID        string
	Customer         string
	IssuedAt         time.Time
	ExpiresAt        time.Time
	Capabilities     domain.Capabilities
	ControlMissingAt *time.Time
	Sync             ControlStatus
}

// Capabilities 返回平台当前生效的能力；未激活授权或授权到期时按免费取值执行。
func Capabilities(ctx context.Context, db bun.IDB) (domain.Capabilities, error) {
	current, err := loadLicense(ctx, db)
	if err != nil {
		return domain.Capabilities{}, err
	}
	return current.EffectiveCapabilities(), nil
}

// EffectiveCapabilities 返回授权当前生效的能力，未激活或已到期时为免费取值。
func (l License) EffectiveCapabilities() domain.Capabilities {
	if l.Status != domain.LicenseStatusActive {
		return domain.FreeCapabilities()
	}
	return l.Capabilities
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
		Where("lic.server_id = (SELECT pf.server_id FROM platforms AS pf LIMIT 1)").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return License{Status: domain.LicenseStatusNone, Capabilities: domain.FreeCapabilities()}, nil
	}
	if err != nil {
		return License{}, fmt.Errorf("load license: %w", err)
	}
	return licenseFromModel(&record.License, record.Active)
}

// licenseFromModel 把授权行转换为授权状态，active 表示授权尚未到期。
func licenseFromModel(record *servermodels.License, active bool) (License, error) {
	var granted map[string]json.RawMessage
	if err := json.Unmarshal(record.Capabilities, &granted); err != nil {
		return License{}, fmt.Errorf("decode license capabilities: %w", err)
	}
	status := domain.LicenseStatusExpired
	if active {
		status = domain.LicenseStatusActive
	}
	return License{
		ServerID: record.ServerID, Status: status, LicenseID: record.LicenseID, Customer: record.Customer,
		IssuedAt: record.IssuedAt, ExpiresAt: record.ExpiresAt,
		Capabilities: capabilitiesFromLicense(granted), ControlMissingAt: record.ControlMissingAt,
	}, nil
}

// capabilitiesFromLicense 在免费取值上应用授权码列出的能力键。
func capabilitiesFromLicense(granted map[string]json.RawMessage) domain.Capabilities {
	capabilities := domain.FreeCapabilities()
	if raw, ok := granted[licensecode.CapabilityWorkspaceLimit]; ok {
		if limit, err := licensecode.WorkspaceLimit(raw); err == nil {
			capabilities.WorkspaceLimit = limit
		}
	}
	if raw, ok := granted[licensecode.CapabilityCustomBranding]; ok {
		if custom, err := licensecode.CustomBranding(raw); err == nil {
			capabilities.CustomBranding = custom
		}
	}
	if raw, ok := granted[licensecode.CapabilityPushRelay]; ok {
		if relay, err := licensecode.PushRelay(raw); err == nil {
			capabilities.PushRelay = relay
		}
	}
	return capabilities
}

// LicenseQuery 读取授权状态。
type LicenseQuery struct {
	db *bun.DB
}

// NewLicenseQuery 创建授权查询。
func NewLicenseQuery(db *bun.DB) *LicenseQuery {
	return &LicenseQuery{db: db}
}

// Execute 返回服务器标识、当前的授权状态与 control 同步结果。
func (q *LicenseQuery) Execute(ctx context.Context) (License, error) {
	platform, err := platformaction.Load(ctx, q.db)
	if err != nil {
		return License{}, err
	}
	current, err := loadLicense(ctx, q.db)
	current.ServerID = platform.ServerID
	current.Sync = ControlStatus{SyncedAt: platform.ControlSyncedAt, FailedAt: platform.ControlFailedAt, Error: platform.ControlError}
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

// storeLicense 在事务内校验服务器标识与授权期限后保存授权：首次激活或签发时间晚于当前授权时写入，与当前授权码相同时原样返回，其余返回 ErrLicenseSuperseded；写入时清空 control 中查不到授权的记录；operator 非空时先确认其仍是有效平台管理员；完成后刷新本实例的部署状态，按新授权应用部署品牌。
func storeLicense(ctx context.Context, db *bun.DB, state platformaction.DeploymentReloader, operator *servermodels.AccountIdentity, claims licensecode.Claims) (License, error) {
	capabilities, err := json.Marshal(claims.Capabilities)
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
		if claims.ServerID != platform.ServerID {
			return ErrLicenseServerMismatch
		}
		var valid bool
		if err := tx.NewRaw("SELECT ?::timestamptz > now()", claims.ExpiresAt).Scan(ctx, &valid); err != nil {
			return err
		}
		if !valid {
			return ErrLicenseExpired
		}
		record := &servermodels.License{ServerID: platform.ServerID}
		err = tx.NewSelect().Model(record).WherePK().Scan(ctx)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// 平台首次激活授权。
		case err != nil:
			return err
		case record.LicenseCode == claims.Code:
			output, err = licenseFromModel(record, true)
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
		record.ControlMissingAt = nil
		if _, err := tx.NewInsert().Model(record).
			Column("server_id", "license_id", "customer", "license_code", "capabilities", "issued_at", "expires_at").
			On("CONFLICT (server_id) DO UPDATE").
			Set("license_id = EXCLUDED.license_id").
			Set("customer = EXCLUDED.customer").
			Set("license_code = EXCLUDED.license_code").
			Set("capabilities = EXCLUDED.capabilities").
			Set("issued_at = EXCLUDED.issued_at").
			Set("expires_at = EXCLUDED.expires_at").
			Set("control_missing_at = NULL").
			Returning("created_at, updated_at").
			Exec(ctx); err != nil {
			return err
		}
		stored = true
		output, err = licenseFromModel(record, true)
		return err
	})
	if err != nil {
		return License{}, err
	}
	if stored {
		slog.InfoContext(ctx, "已保存授权", "license_id", claims.LicenseID, "issued_at", claims.IssuedAt, "expires_at", claims.ExpiresAt)
	}
	// 授权已保存，刷新失败时由下次心跳刷新。
	if err := state.Reload(context.WithoutCancel(ctx)); err != nil {
		slog.WarnContext(ctx, "保存授权后刷新部署状态失败", "error", err)
	}
	return output, nil
}
