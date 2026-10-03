//go:build server

package agentrun

import (
	"context"

	websearchaction "github.com/runforyou-ai/luway/internal/actions/websearch"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	"github.com/uptrace/bun"
)

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
