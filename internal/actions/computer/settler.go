//go:build server

package computer

import (
	"context"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ToolCallSettler 在派发到电脑的调用有结果或本机 Agent 请求权限时推进工具决定与等待结果的运行，由组合根注入。
type ToolCallSettler interface {
	// OnToolCallSettled 在一次调用写入结果的事务中通知负责人并唤醒等待结果的一方，调用方已按会话、调用的顺序加锁；
	// review 表示调用进入或离开待核对，amended 表示调用此前已按结果未知结算、本次只补记实际结果。
	OnToolCallSettled(ctx context.Context, tx bun.Tx, call *servermodels.AgentToolCall, review, amended bool) error
	// OnLostToolCallsSettled 在同一运行的已丢失调用批量结算的事务中通知负责人并唤醒等待结果的一方，调用方已锁定运行所属会话；review 表示有调用转为待核对。
	OnLostToolCallsSettled(ctx context.Context, tx bun.Tx, workspaceID, runID string, calls []servermodels.AgentToolCall, review bool) error
	// RequestLocalAgentPermission 为执行中的委派本机 Agent 的一轮记录权限请求，交给这一轮的发起人确认。
	RequestLocalAgentPermission(ctx context.Context, workspaceID, computerID, turnID, requestID string, permission domain.LocalAgentPermission) error
}
