//go:build server

package agentrun

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/agentcancel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// RunCancellation 结束执行范围内在途的 Agent 运行：成员停止回复，以及客服周期、群成员与群解散变化时取消运行并安排下一次运行。
type RunCancellation struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewRunCancellation 创建 Agent 运行的停止与取消处理。
func NewRunCancellation(db *bun.DB, enqueuer servertask.TxEnqueuer) *RunCancellation {
	return &RunCancellation{db: db, enqueuer: enqueuer}
}

// CancelForServiceSession 在客服事务内取消原负责人尚未结束的运行。
func (c *RunCancellation) CancelForServiceSession(ctx context.Context, db bun.IDB, workspaceID, serviceSessionID, agentIdentityID string, reason domain.AgentRunErrorCode) ([]string, error) {
	return agentcancel.CancelServiceSessionRuns(ctx, db, workspaceID, serviceSessionID, agentIdentityID, reason)
}

// CancelForGroupAgent 在成员变化事务内取消群内指定 Agent 的在途运行、结算其输入队列并轮转到下一位。
func (c *RunCancellation) CancelForGroupAgent(ctx context.Context, db bun.IDB, workspaceID, conversationID, agentIdentityID string) error {
	if err := agentcancel.CancelGroupAgentRuns(ctx, db, workspaceID, conversationID, agentIdentityID); err != nil {
		return err
	}
	return c.rotateGroupScope(ctx, db, workspaceID, conversationID)
}

// rotateGroupScope 在群会话事务内为让出的活动运行名额安排下一位 AI 员工。
func (c *RunCancellation) rotateGroupScope(ctx context.Context, db bun.IDB, workspaceID, conversationID string) error {
	conversation, err := chatstate.LockConversation(ctx, db, workspaceID, conversationID)
	if err != nil {
		return err
	}
	policy := groupMentionRunPolicy{scheduler: NewScheduler(c.enqueuer)}
	return scheduleNextRun(ctx, db, c.enqueuer, policy, agentRunPolicyContext{Conversation: conversation},
		workspaceID, domain.AgentExecutionScopeConversation, conversationID)
}

// CancelForGroupConversation 在群解散事务内取消群内全部 Agent 的在途运行。
func (c *RunCancellation) CancelForGroupConversation(ctx context.Context, db bun.IDB, workspaceID, conversationID string) error {
	return agentcancel.CancelGroupRuns(ctx, db, workspaceID, conversationID)
}
