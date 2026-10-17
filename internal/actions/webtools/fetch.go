//go:build server

package webtools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	integrationwebfetch "github.com/runforyou-ai/luway/internal/integration/webfetch"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	pkgwebfetch "github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
)

// 网页读取设置的字段校验码。
const (
	ValidationFetchProviderInvalid common.FieldCode = "WEB_FETCH_PROVIDER_INVALID"
	ValidationFetchAPIKeyRequired  common.FieldCode = "WEB_FETCH_API_KEY_REQUIRED"
	ValidationFetchBaseURLInvalid  common.FieldCode = "WEB_FETCH_BASE_URL_INVALID"
)

// Fetcher 按配置读取网页。
type Fetcher interface {
	Fetch(context.Context, integrationwebfetch.Config, string) (pkgwebfetch.Document, error)
	Probe(context.Context, integrationwebfetch.Config) error
}

// LoadFetchConfig 读取企业的网页读取配置；未配置时返回内置直连。
func LoadFetchConfig(ctx context.Context, db bun.IDB, workspaceID string) (integrationwebfetch.Config, error) {
	setting := &servermodels.WebFetchSetting{}
	err := db.NewSelect().Model(setting).Where("wfs.workspace_id = ?", workspaceID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationwebfetch.Config{Provider: domain.WebFetchProviderDirect}, nil
	}
	if err != nil {
		return integrationwebfetch.Config{}, fmt.Errorf("load web fetch settings: %w", err)
	}
	return integrationwebfetch.Config{
		Provider: domain.WebFetchProvider(setting.Provider), APIKey: setting.APIKey, BaseURL: setting.BaseURL,
	}, nil
}

// normalizeFetchConfig 规范化并校验网页读取配置：内置直连不需要凭据，外部服务商需要 API Key，实例地址可选。
func normalizeFetchConfig(config integrationwebfetch.Config) (integrationwebfetch.Config, map[string]common.FieldCode) {
	fields := make(map[string]common.FieldCode)
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	if !domain.ValidWebFetchProvider(config.Provider) {
		fields["provider"] = ValidationFetchProviderInvalid
		return config, fields
	}
	if config.Provider == domain.WebFetchProviderDirect {
		config.APIKey = ""
		config.BaseURL = ""
		return config, fields
	}
	if config.APIKey == "" {
		fields["apiKey"] = ValidationFetchAPIKeyRequired
	}
	if config.BaseURL != "" {
		parsed, err := url.Parse(config.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			fields["baseUrl"] = ValidationFetchBaseURLInvalid
		} else {
			config.BaseURL = strings.TrimRight(config.BaseURL, "/")
		}
	}
	return config, fields
}

// GetFetchSettings 返回当前企业的网页读取配置。
func GetFetchSettings(ctx context.Context, db bun.IDB, identity *servermodels.Identity) (integrationwebfetch.Config, error) {
	return LoadFetchConfig(ctx, db, identity.Workspace.ID)
}

// UpdateFetchSettings 校验并保存读取服务配置；保存内置直连时删除记录以回到默认。
func UpdateFetchSettings(ctx context.Context, db *bun.DB, identity *servermodels.Identity, config integrationwebfetch.Config) (integrationwebfetch.Config, error) {
	normalized, fields := normalizeFetchConfig(config)
	if len(fields) > 0 {
		return integrationwebfetch.Config{}, &common.FieldError{Fields: fields}
	}
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if normalized.Provider == domain.WebFetchProviderDirect {
			if _, err := tx.NewDelete().Model((*servermodels.WebFetchSetting)(nil)).
				Where("workspace_id = ?", identity.Workspace.ID).Exec(ctx); err != nil {
				return fmt.Errorf("delete web fetch settings: %w", err)
			}
			return nil
		}
		setting := &servermodels.WebFetchSetting{
			WorkspaceID: identity.Workspace.ID, Provider: string(normalized.Provider),
			APIKey: normalized.APIKey, BaseURL: normalized.BaseURL,
		}
		if _, err := tx.NewInsert().Model(setting).
			Column("workspace_id", "provider", "api_key", "base_url").
			On("CONFLICT (workspace_id) DO UPDATE").
			Set("provider = EXCLUDED.provider").
			Set("api_key = EXCLUDED.api_key").
			Set("base_url = EXCLUDED.base_url").
			Exec(ctx); err != nil {
			return fmt.Errorf("save web fetch settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return integrationwebfetch.Config{}, err
	}
	return normalized, nil
}

// TestFetch 校验草稿配置并读取一次固定公开页。
func TestFetch(ctx context.Context, runner *connectiontest.Runner, fetcher Fetcher, config integrationwebfetch.Config) error {
	config, fields := normalizeFetchConfig(config)
	if len(fields) > 0 {
		return &common.FieldError{Fields: fields}
	}
	return runner.Run(ctx, connectiontest.Target{
		Category: string(domain.ConnectionProbeWebFetch), Adapter: string(config.Provider), Location: string(domain.ConnectionProbeServer),
	}, connectiontest.ProbeFunc(func(ctx context.Context) error {
		return fetcher.Probe(ctx, config)
	}))
}

// WorkspaceFetcher 按工作区当前设置读取网页。
type WorkspaceFetcher struct {
	db      bun.IDB
	fetcher Fetcher
}

// NewWorkspaceFetcher 创建按工作区设置读取网页的装配器。
func NewWorkspaceFetcher(db bun.IDB, fetcher Fetcher) *WorkspaceFetcher {
	return &WorkspaceFetcher{db: db, fetcher: fetcher}
}

// Fetch 加载工作区配置后读取页面。
func (f *WorkspaceFetcher) Fetch(ctx context.Context, workspaceID, target string) (pkgwebfetch.Document, error) {
	config, err := LoadFetchConfig(ctx, f.db, workspaceID)
	if err != nil {
		return pkgwebfetch.Document{}, err
	}
	return f.fetcher.Fetch(ctx, config, target)
}
