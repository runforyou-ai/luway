//go:build server

package tooldecision

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/businesssystem"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RunScope 是按运行策略锁定的运行所属会话上下文，以及以结果事件唤醒该执行范围的能力。
type RunScope interface {
	// Conversation 返回锁定的会话。
	Conversation() *servermodels.Conversation
	// ServiceSession 返回锁定的服务周期，非服务周期执行范围为空。
	ServiceSession() *servermodels.ServiceSession
	// AgentParticipantID 返回锁定时取得的 AI 员工参与者，未取得时为空。
	AgentParticipantID() string
	// Wake 把会话中的结果事件消息作为事件输入追加到运行所属执行范围，并安排执行范围的下一次运行。
	Wake(ctx context.Context, tx bun.Tx, run *servermodels.AgentRun, messageID string) error
}

// RunScopes 是工具决定所需的运行能力，由 Agent 运行实现并由组合根注入：按运行策略锁定运行所属会话、恢复挂起的运行与读取运行的可信上下文值。
type RunScopes interface {
	// Lock 按运行策略锁定运行所属会话上下文。
	Lock(ctx context.Context, tx bun.Tx, run *servermodels.AgentRun) (RunScope, error)
	// ResumeWaitingRun 在挂起运行的工具调用全部有结果时恢复运行，调用方在写入调用结果的事务中调用。
	ResumeWaitingRun(ctx context.Context, db bun.IDB, workspaceID, runID string) error
	// RunValues 读取运行能提供的可信上下文值。
	RunValues(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (businesssystem.RunValues, error)
}
