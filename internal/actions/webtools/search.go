//go:build server

// Package webtools 维护企业联网搜索与网页读取设置并测试连接。
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
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// 联网搜索设置的字段校验码。
const (
	ValidationSearchProviderInvalid common.FieldCode = "WEB_SEARCH_PROVIDER_INVALID"
	ValidationSearchAPIKeyRequired  common.FieldCode = "WEB_SEARCH_API_KEY_REQUIRED"
	ValidationSearchBaseURLRequired common.FieldCode = "WEB_SEARCH_BASE_URL_REQUIRED"
	ValidationSearchBaseURLInvalid  common.FieldCode = "WEB_SEARCH_BASE_URL_INVALID"
)

// Searcher 调用搜索服务。
type Searcher interface {
	Search(context.Context, websearch.Config, websearch.Request) (websearch.Result, error)
}

// LoadSearchConfig 读取企业的联网搜索配置，未启用时返回 nil。
func LoadSearchConfig(ctx context.Context, db bun.IDB, workspaceID string) (*websearch.Config, error) {
	setting := &servermodels.WebSearchSetting{}
	err := db.NewSelect().Model(setting).Where("wss.workspace_id = ?", workspaceID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load web search settings: %w", err)
	}
	return &websearch.Config{Provider: domain.WebSearchProvider(setting.Provider), APIKey: setting.APIKey, BaseURL: setting.BaseURL}, nil
}

// normalizeSearchConfig 规范化并校验搜索服务配置：自托管服务只需要实例地址，其他服务商只需要 API Key。
func normalizeSearchConfig(config websearch.Config) (websearch.Config, map[string]common.FieldCode) {
	fields := make(map[string]common.FieldCode)
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	if !domain.ValidWebSearchProvider(config.Provider) {
		fields["provider"] = ValidationSearchProviderInvalid
		return config, fields
	}
	if !domain.WebSearchProviderSelfHosted(config.Provider) {
		config.BaseURL = ""
		if config.APIKey == "" {
			fields["apiKey"] = ValidationSearchAPIKeyRequired
		}
		return config, fields
	}
	config.APIKey = ""
	// 实例地址只接受 http 与 https 的绝对地址，去掉末尾斜杠。
	parsed, err := url.Parse(config.BaseURL)
	switch {
	case config.BaseURL == "":
		fields["baseUrl"] = ValidationSearchBaseURLRequired
	case err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "":
		fields["baseUrl"] = ValidationSearchBaseURLInvalid
	default:
		config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	}
	return config, fields
}

// GetSearchSettings 返回当前企业的联网搜索配置，未启用时为 nil。
func GetSearchSettings(ctx context.Context, db bun.IDB, identity *servermodels.Identity) (*websearch.Config, error) {
	return LoadSearchConfig(ctx, db, identity.Workspace.ID)
}

// UpdateSearchSettings 校验并保存搜索服务配置，nil 表示关闭联网搜索。
func UpdateSearchSettings(ctx context.Context, db *bun.DB, identity *servermodels.Identity, config *websearch.Config) (*websearch.Config, error) {
	if config != nil {
		normalized, fields := normalizeSearchConfig(*config)
		if len(fields) > 0 {
			return nil, &common.FieldError{Fields: fields}
		}
		config = &normalized
	}
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if config == nil {
			if _, err := tx.NewDelete().Model((*servermodels.WebSearchSetting)(nil)).
				Where("workspace_id = ?", identity.Workspace.ID).Exec(ctx); err != nil {
				return fmt.Errorf("delete web search settings: %w", err)
			}
			return nil
		}
		setting := &servermodels.WebSearchSetting{
			WorkspaceID: identity.Workspace.ID, Provider: string(config.Provider), APIKey: config.APIKey, BaseURL: config.BaseURL,
		}
		if _, err := tx.NewInsert().Model(setting).
			Column("workspace_id", "provider", "api_key", "base_url").
			On("CONFLICT (workspace_id) DO UPDATE").
			Set("provider = EXCLUDED.provider").
			Set("api_key = EXCLUDED.api_key").
			Set("base_url = EXCLUDED.base_url").
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

// TestSearch 校验草稿配置并搜索一次固定关键词。
func TestSearch(ctx context.Context, runner *connectiontest.Runner, searcher Searcher, config websearch.Config) error {
	config, fields := normalizeSearchConfig(config)
	if len(fields) > 0 {
		return &common.FieldError{Fields: fields}
	}
	return runner.Run(ctx, connectiontest.Target{
		Category: string(domain.ConnectionProbeWebSearch), Adapter: string(config.Provider), Location: string(domain.ConnectionProbeServer),
	}, connectiontest.ProbeFunc(func(ctx context.Context) error {
		_, err := searcher.Search(ctx, config, websearch.Request{Query: "test", Count: 1})
		return err
	}))
}
