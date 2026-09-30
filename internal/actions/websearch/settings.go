//go:build server

// Package websearch 维护企业联网搜索设置并测试搜索服务。
package websearch

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
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// ValidationCode 标识联网搜索设置字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationProviderInvalid ValidationCode = "WEB_SEARCH_PROVIDER_INVALID"
	ValidationAPIKeyRequired  ValidationCode = "WEB_SEARCH_API_KEY_REQUIRED"
	ValidationBaseURLRequired ValidationCode = "WEB_SEARCH_BASE_URL_REQUIRED"
	ValidationBaseURLInvalid  ValidationCode = "WEB_SEARCH_BASE_URL_INVALID"
)

// ValidationError 表示联网搜索设置校验失败。
type ValidationError = common.FieldError

// Searcher 调用搜索服务。
type Searcher interface {
	Search(context.Context, websearch.Config, websearch.Request) (websearch.Result, error)
}

// LoadConfig 读取企业的联网搜索配置，未启用时返回 nil。
func LoadConfig(ctx context.Context, db bun.IDB, organizationID string) (*websearch.Config, error) {
	setting := &servermodels.WebSearchSetting{}
	err := db.NewSelect().Model(setting).Where("wss.organization_id = ?", organizationID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load web search settings: %w", err)
	}
	return &websearch.Config{Provider: domain.WebSearchProvider(setting.Provider), APIKey: setting.APIKey, BaseURL: setting.BaseURL}, nil
}

// normalizeConfig 规范化并校验搜索服务配置：自托管服务只需要实例地址，其他服务商只需要 API Key。
func normalizeConfig(config websearch.Config) (websearch.Config, map[string]ValidationCode) {
	fields := make(map[string]ValidationCode)
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	if !domain.ValidWebSearchProvider(config.Provider) {
		fields["provider"] = ValidationProviderInvalid
		return config, fields
	}
	if !domain.WebSearchProviderSelfHosted(config.Provider) {
		config.BaseURL = ""
		if config.APIKey == "" {
			fields["apiKey"] = ValidationAPIKeyRequired
		}
		return config, fields
	}
	config.APIKey = ""
	// 实例地址只接受 http 与 https 的绝对地址，去掉末尾斜杠。
	parsed, err := url.Parse(config.BaseURL)
	switch {
	case config.BaseURL == "":
		fields["baseUrl"] = ValidationBaseURLRequired
	case err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "":
		fields["baseUrl"] = ValidationBaseURLInvalid
	default:
		config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	}
	return config, fields
}

// GetSettingsQuery 读取当前企业的联网搜索设置。
type GetSettingsQuery struct {
	db *bun.DB
}

// NewGetSettingsQuery 创建联网搜索设置读取查询。
func NewGetSettingsQuery(db *bun.DB) *GetSettingsQuery {
	return &GetSettingsQuery{db: db}
}

// Execute 返回当前企业的联网搜索配置，未启用时为 nil。
func (q *GetSettingsQuery) Execute(ctx context.Context, identity *servermodels.Identity) (*websearch.Config, error) {
	return LoadConfig(ctx, q.db, identity.Organization.ID)
}

// UpdateSettingsAction 修改当前企业的联网搜索设置。
type UpdateSettingsAction struct {
	db *bun.DB
}

// NewUpdateSettingsAction 创建联网搜索设置修改操作。
func NewUpdateSettingsAction(db *bun.DB) *UpdateSettingsAction {
	return &UpdateSettingsAction{db: db}
}

// Execute 校验并保存搜索服务配置，nil 表示关闭联网搜索。
func (a *UpdateSettingsAction) Execute(ctx context.Context, identity *servermodels.Identity, config *websearch.Config) (*websearch.Config, error) {
	if config != nil {
		normalized, fields := normalizeConfig(*config)
		if len(fields) > 0 {
			return nil, &ValidationError{Fields: fields}
		}
		config = &normalized
	}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if config == nil {
			if _, err := tx.NewDelete().Model((*servermodels.WebSearchSetting)(nil)).
				Where("organization_id = ?", identity.Organization.ID).Exec(ctx); err != nil {
				return fmt.Errorf("delete web search settings: %w", err)
			}
			return nil
		}
		setting := &servermodels.WebSearchSetting{
			OrganizationID: identity.Organization.ID, Provider: string(config.Provider), APIKey: config.APIKey, BaseURL: config.BaseURL,
		}
		if _, err := tx.NewInsert().Model(setting).
			Column("organization_id", "provider", "api_key", "base_url").
			On("CONFLICT (organization_id) DO UPDATE").
			Set("provider = EXCLUDED.provider").
			Set("api_key = EXCLUDED.api_key").
			Set("base_url = EXCLUDED.base_url").
			Set("updated_at = now()").
			Exec(ctx); err != nil {
			return fmt.Errorf("save web search settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return config, nil
}

// TestAction 用草稿配置执行一次搜索，验证搜索服务可用。
type TestAction struct {
	runner   *connectiontest.Runner
	searcher Searcher
}

// NewTestAction 创建搜索服务测试操作。
func NewTestAction(runner *connectiontest.Runner, searcher Searcher) *TestAction {
	return &TestAction{runner: runner, searcher: searcher}
}

// Execute 校验草稿配置并搜索一次固定关键词。
func (a *TestAction) Execute(ctx context.Context, config websearch.Config) error {
	config, fields := normalizeConfig(config)
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return a.runner.Run(ctx, connectiontest.Target{
		Category: string(domain.ConnectionProbeWebSearch), Adapter: string(config.Provider), Location: string(domain.ConnectionProbeServer),
	}, connectiontest.ProbeFunc(func(ctx context.Context) error {
		_, err := a.searcher.Search(ctx, config, websearch.Request{Query: "test", Count: 1})
		return err
	}))
}
