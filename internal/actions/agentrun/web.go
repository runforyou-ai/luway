//go:build server

package agentrun

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/webtools"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	pkgwebfetch "github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
)

// loadRunWebSearch 按企业当前的联网搜索设置构造搜索函数，企业未启用联网搜索时返回 nil。
func loadRunWebSearch(ctx context.Context, db bun.IDB, searcher webtools.Searcher, workspaceID string) (agentruntime.WebSearch, error) {
	config, err := webtools.LoadSearchConfig(ctx, db, workspaceID)
	if err != nil || config == nil {
		return nil, err
	}
	return func(ctx context.Context, request websearch.Request) (websearch.Result, error) {
		return searcher.Search(ctx, *config, request)
	}, nil
}

// loadRunWebFetch 按企业当前的网页读取设置构造读取函数，未配置时使用内置直连。
func loadRunWebFetch(ctx context.Context, db bun.IDB, fetcher webtools.Fetcher, workspaceID string) (agentruntime.WebFetch, error) {
	config, err := webtools.LoadFetchConfig(ctx, db, workspaceID)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, target string) (pkgwebfetch.Document, error) {
		return fetcher.Fetch(ctx, config, target)
	}, nil
}
