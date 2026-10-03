//go:build server

package platform

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
	// ErrLicenseServerMismatch 表示授权码绑定的是其他服务器。
	ErrLicenseServerMismatch = errors.New("license code belongs to another server")
	// ErrLicenseExpired 表示授权码已过授权期限。
	ErrLicenseExpired = errors.New("license code expired")
	// ErrLicenseSuperseded 表示授权码的签发时间不晚于当前授权。
	ErrLicenseSuperseded = errors.New("license code is not newer than the current license")
)

// License 定义服务器标识与授权状态；Capabilities 是授权码授予的能力，授权到期后平台按免费取值运行。
type License struct {
	ServerID     string
	Status       domain.LicenseStatus
	LicenseID    string
	Customer     string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	Capabilities domain.Capabilities
}

// Capabilities 返回平台当前生效的能力；未激活授权或授权到期时按免费取值执行。
func Capabilities(ctx context.Context, db bun.IDB) (domain.Capabilities, error) {
	current, err := loadLicense(ctx, db)
	if err != nil {
		return domain.Capabilities{}, err
	}
	if current.Status != domain.LicenseStatusActive {
		return domain.FreeCapabilities(), nil
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

// loadLicense 读取平台当前的授权并按当前时间判断状态。
func loadLicense(ctx context.Context, db bun.IDB) (License, error) {
	record := &servermodels.License{}
	err := db.NewSelect().Model(record).
		Where("lic.server_id = (SELECT pf.server_id FROM platforms AS pf LIMIT 1)").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return License{Status: domain.LicenseStatusNone, Capabilities: domain.FreeCapabilities()}, nil
	}
	if err != nil {
		return License{}, fmt.Errorf("load license: %w", err)
	}
	return licenseFromModel(record, time.Now())
}

// licenseFromModel 把授权行转换为授权状态，按 now 判断是否到期。
func licenseFromModel(record *servermodels.License, now time.Time) (License, error) {
	var granted map[string]json.RawMessage
	if err := json.Unmarshal(record.Capabilities, &granted); err != nil {
		return License{}, fmt.Errorf("decode license capabilities: %w", err)
	}
	status := domain.LicenseStatusActive
	if !now.Before(record.ExpiresAt) {
		status = domain.LicenseStatusExpired
	}
	return License{
		ServerID: record.ServerID, Status: status, LicenseID: record.LicenseID, Customer: record.Customer,
		IssuedAt: record.IssuedAt, ExpiresAt: record.ExpiresAt,
		Capabilities: capabilitiesFromLicense(granted),
	}, nil
}

// capabilitiesFromLicense 在免费取值上应用授权码列出的能力键。
func capabilitiesFromLicense(granted map[string]json.RawMessage) domain.Capabilities {
	capabilities := domain.FreeCapabilities()
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

// LicenseQuery 读取授权状态。
type LicenseQuery struct {
	db *bun.DB
}

// NewLicenseQuery 创建授权查询。
func NewLicenseQuery(db *bun.DB) *LicenseQuery {
	return &LicenseQuery{db: db}
}

// Execute 返回服务器标识与当前的授权状态。
func (q *LicenseQuery) Execute(ctx context.Context) (License, error) {
	platform, err := Load(ctx, q.db)
	if err != nil {
		return License{}, err
	}
	current, err := loadLicense(ctx, q.db)
	current.ServerID = platform.ServerID
	return current, err
}

// ActivateLicenseAction 用授权码激活或替换授权。
type ActivateLicenseAction struct {
	db   *bun.DB
	keys license.Keys
}

// NewActivateLicenseAction 创建授权激活操作，keys 是用于验签的 control 签名公钥。
func NewActivateLicenseAction(db *bun.DB, keys license.Keys) *ActivateLicenseAction {
	return &ActivateLicenseAction{db: db, keys: keys}
}

// Execute 验签授权码后保存为授权，签发时间不晚于当前授权的其他授权码返回 ErrLicenseSuperseded。
func (a *ActivateLicenseAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, code string) (License, error) {
	claims, err := license.Parse(code, a.keys)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrLicenseInvalid, err)
	}
	return storeLicense(ctx, a.db, operator, claims)
}

// storeLicense 在事务内校验服务器标识与授权期限后保存授权：首次激活或签发时间晚于当前授权时写入，与当前授权码相同时原样返回，其余返回 ErrLicenseSuperseded；operator 非空时先确认其仍是有效平台管理员；完成后按新授权应用部署品牌。
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
		platform, err := Lock(ctx, tx)
		if err != nil {
			return err
		}
		if claims.InstanceID != platform.ServerID {
			return ErrLicenseServerMismatch
		}
		now := time.Now()
		if !now.Before(claims.ExpiresAt) {
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
			Column("server_id", "license_id", "customer", "license_code", "capabilities", "issued_at", "expires_at").
			On("CONFLICT (server_id) DO UPDATE").
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
