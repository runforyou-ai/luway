//go:build server

package tooldecision

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// List 按提交时间倒序返回待当前成员确认、审批或核对的工具调用。
func (a *Action) List(ctx context.Context, identity *servermodels.Identity) ([]agentprocess.ToolDecision, error) {
	return agentprocess.ListPendingToolDecisions(ctx, a.db, identity)
}
