//go:build server

package tooldecision

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// OnToolCallSettled 在派发到电脑的一次调用写入结果的事务中推进后续处理，调用方已按会话、调用的顺序加锁：review 为真时先通知所属 AI 员工的负责人刷新待处理；
// amended 为真表示调用此前已按结果未知结算、本次只补记实际结果，不写入会话也不唤醒运行；其余情况下委派本机 Agent 的轮次以 AI 员工身份把结果写入会话，
// 提交后批准执行的调用以结果事件唤醒提交它的 AI 员工，其余调用（含暂停确认后在运行内执行的调用）唤醒等待结果的运行。
func (a *Action) OnToolCallSettled(ctx context.Context, tx bun.Tx, call *servermodels.AgentToolCall, review, amended bool) error {
	if review {
		if err := agentprocess.NotifyReviewers(ctx, tx, call.WorkspaceID, call.AgentRunID); err != nil {
			return err
		}
	}
	switch {
	case amended:
		return nil
	case call.LocalAgentSessionID != nil:
		return a.finishLocalAgentTurn(ctx, tx, call.WorkspaceID, call.ID)
	case agentprocess.Submitted(call):
		return a.resolveApprovedCall(ctx, tx, call.WorkspaceID, call.ID)
	default:
		return a.scopes.ResumeWaitingRun(ctx, tx, call.WorkspaceID, call.AgentRunID)
	}
}

// OnLostToolCallsSettled 在同一运行派发到电脑的已丢失调用批量结算的事务中推进后续处理，调用方已锁定运行所属会话：review 为真时通知一次负责人刷新待处理；
// 先以结果事件唤醒批准后执行的调用，再把委派本机 Agent 的轮次结果写入会话，最后唤醒等待这些结果的运行。
func (a *Action) OnLostToolCallsSettled(ctx context.Context, tx bun.Tx, workspaceID, runID string, calls []servermodels.AgentToolCall, review bool) error {
	if review {
		if err := agentprocess.NotifyReviewers(ctx, tx, workspaceID, runID); err != nil {
			return err
		}
	}
	approved, turns := make([]string, 0), make([]string, 0)
	for _, call := range calls {
		switch {
		case call.LocalAgentSessionID != nil:
			turns = append(turns, call.ID)
		case agentprocess.Submitted(&call):
			approved = append(approved, call.ID)
		}
	}
	for _, callID := range approved {
		if err := a.resolveApprovedCall(ctx, tx, workspaceID, callID); err != nil {
			return err
		}
	}
	for _, callID := range turns {
		if err := a.finishLocalAgentTurn(ctx, tx, workspaceID, callID); err != nil {
			return err
		}
	}
	return a.scopes.ResumeWaitingRun(ctx, tx, workspaceID, runID)
}
