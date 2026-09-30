//go:build server

package agentrun

import (
	"context"
	"errors"
	"fmt"

	websearchaction "github.com/runforyou-ai/cervi/internal/actions/websearch"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"github.com/runforyou-ai/cervi/internal/integration/websearch"
	"github.com/uptrace/bun"
)

// ErrWebSearchDisabled 表示企业已关闭联网搜索。
var ErrWebSearchDisabled = errors.New("web search is disabled for this organization")

// loadRunWebSearch 按企业当前的联网搜索设置构造搜索函数，企业未启用联网搜索时返回 nil。
func loadRunWebSearch(ctx context.Context, db bun.IDB, searcher websearchaction.Searcher, organizationID string) (agentruntime.WebSearch, error) {
	config, err := websearchaction.LoadConfig(ctx, db, organizationID)
	if err != nil || config == nil {
		return nil, err
	}
	return func(ctx context.Context, request websearch.Request) (websearch.Result, error) {
		return searcher.Search(ctx, *config, request)
	}, nil
}

// SearchDeviceRunWeb 用企业当前配置的搜索服务为设备持有的运行搜索互联网。
func (a *ExecuteAction) SearchDeviceRunWeb(ctx context.Context, device RunDevice, runID string, request websearch.Request) (websearch.Result, error) {
	run, err := a.requireDeviceLease(ctx, device, runID)
	if err != nil {
		return websearch.Result{}, err
	}
	search, err := loadRunWebSearch(ctx, a.db, a.webSearch, run.OrganizationID)
	if err != nil {
		return websearch.Result{}, fmt.Errorf("load device agent run web search: %w", err)
	}
	if search == nil {
		return websearch.Result{}, ErrWebSearchDisabled
	}
	return search(ctx, request)
}
