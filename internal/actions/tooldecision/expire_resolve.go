//go:build server

package tooldecision

import (
	"context"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/uptrace/bun"
)

// Expire 把截止时仍等待确认或审批的工具调用记为过期，并以结果事件唤醒提交它的 AI 员工；暂停运行等待确认的调用过期时记为拒绝并恢复运行。
func (a *Action) Expire(ctx context.Context, input agentprocess.ToolCallInput) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := a.lockToolCall(ctx, tx, input.WorkspaceID, input.ToolCallID)
		if errors.Is(err, ErrToolCallUnavailable) {
			return nil
		}
		if err != nil {
			return err
		}
		call := locked.Call
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		if call.Status != string(domain.AgentToolCallAwaitingDecision) || len(call.Decision) > 0 || call.ExpiresAt == nil || call.ExpiresAt.After(now) {
			return nil
		}
		// 暂停运行等待确认的调用过期即拒绝，运行恢复后把原因交给模型。
		if paused(call) {
			return a.decidePaused(ctx, tx, locked, nil, agentcontract.CallDecision{Reason: "确认已过期，操作没有执行。"})
		}
		if _, err := tx.NewUpdate().Model(call).Set("decided_at = now()").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("expire agent tool call: %w", err)
		}
		return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallExpired, nil, nil)
	})
}

// Resolve 为已取消的工具调用写入结果事件，并在执行范围仍由提交它的 AI 员工处理时唤醒它；暂停运行等待确认的调用取消后恢复运行。
func (a *Action) Resolve(ctx context.Context, input agentprocess.ToolCallInput) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := a.lockToolCall(ctx, tx, input.WorkspaceID, input.ToolCallID)
		if errors.Is(err, ErrToolCallUnavailable) {
			return nil
		}
		if err != nil || locked.Call.Status != string(domain.AgentToolCallCancelled) {
			return err
		}
		// 本机 Agent 的权限请求取消后由执行器按取消处理，不唤醒 AI 员工。
		if locked.Call.Source == string(domain.AgentToolSourceLocalAgent) {
			return settleLocalAgentPermission(ctx, tx, locked)
		}
		// 暂停运行等待确认的调用取消后记下交给模型的说明并恢复运行。
		if locked.Call.Handover == nil {
			if _, err := tx.NewUpdate().Model(locked.Call).Set("result = ?", pausedCancelledResult).WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("record cancelled paused agent tool call: %w", err)
			}
			return a.scopes.ResumeWaitingRun(ctx, tx, locked.Call.WorkspaceID, locked.Run.ID)
		}
		return a.resolveToolCall(ctx, tx, locked)
	})
}
