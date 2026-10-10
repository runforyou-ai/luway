//go:build server

package computer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// SweepActionName 是结算已丢失电脑调用的定时 Action 名称。
const SweepActionName = "computer.sweep"

// reportGrace 是电脑操作执行时限到达后等待执行器上报结果的宽限。
const reportGrace = time.Minute

// unsettledStatuses 是派发到电脑后尚未结束的调用状态：待领取与执行中。
var unsettledStatuses = []domain.AgentToolCallStatus{domain.AgentToolCallQueued, domain.AgentToolCallRunning}

// lostCondition 返回派发到电脑的未结束调用已丢失的判断条件及其参数：电脑已撤销；尚未领取而电脑离线；已领取超过执行时限与上报宽限仍未上报结果，与电脑是否在线无关，委派本机 Agent 的轮次不设时限。
func lostCondition() (string, []any) {
	return `(cmp.revoked_at IS NOT NULL
		OR (atc.status = ? AND (cmp.last_seen_at IS NULL OR cmp.last_seen_at < now() - make_interval(secs => ?)))
		OR (atc.status = ? AND atc.local_agent_session_id IS NULL AND atc.started_at < now() - make_interval(secs => ?)))`,
		[]any{domain.AgentToolCallQueued, domain.ComputerPresenceTimeout.Seconds(),
			domain.AgentToolCallRunning, (domain.ComputerOperationTimeout + reportGrace).Seconds()}
}

// SweepAction 结算已撤销电脑、离线电脑上尚未领取以及超过执行时限的调用。
type SweepAction struct {
	db      *bun.DB
	settler ToolCallSettler
}

// NewSweepAction 创建电脑调用丢失结算，settler 在结算后唤醒等待结果的一方。
func NewSweepAction(db *bun.DB, settler ToolCallSettler) *SweepAction {
	return &SweepAction{db: db, settler: settler}
}

// Execute 找出派发到电脑且已丢失的调用，逐个运行结算并唤醒等待的运行。
func (a *SweepAction) Execute(ctx context.Context, _ struct{}) error {
	condition, args := lostCondition()
	return settleMatchingCalls(ctx, a.db, a.settler, false, condition, args...)
}

// settleMatchingCalls 找出满足条件的派发到电脑且未结束的调用，按运行逐个结算并唤醒等待的运行；条件中电脑别名为 cmp、调用别名为 atc；replay 为 true 时，所属运行未结束、可重新执行且领取次数未达上限的已领取调用重新派发。
func settleMatchingCalls(ctx context.Context, db *bun.DB, settler ToolCallSettler, replay bool, condition string, args ...any) error {
	var runs []struct {
		WorkspaceID string `bun:"workspace_id"`
		AgentRunID  string `bun:"agent_run_id"`
	}
	if err := db.NewRaw(`
		SELECT DISTINCT atc.workspace_id::text, atc.agent_run_id::text
		FROM agent_tool_calls AS atc
		JOIN computers AS cmp ON cmp.id = atc.computer_id
		WHERE atc.status IN (?) AND `+condition,
		append([]any{bun.List(unsettledStatuses)}, args...)...).Scan(ctx, &runs); err != nil {
		return fmt.Errorf("list lost computer operations: %w", err)
	}
	for _, run := range runs {
		if err := settleLostCalls(ctx, db, settler, run.WorkspaceID, run.AgentRunID, replay, condition, args...); err != nil {
			return err
		}
	}
	return nil
}

// settleLostCalls 在运行所属会话的锁内处理该运行满足条件的派发到电脑且未结束的调用，并唤醒等待这些结果的运行，批准后执行的调用以结果事件唤醒提交它的 AI 员工：replay 为 true 时，批准后执行的调用或所属运行未结束的调用中，可重新执行且领取次数未达上限的已领取调用回到待领取；其余尚未领取的调用失败，已领取的调用按可重新执行与外部副作用中断或待核对；委派本机 Agent 的轮次已开始的记为中断，并把结果写入会话。
func settleLostCalls(ctx context.Context, db *bun.DB, settler ToolCallSettler, workspaceID, runID string, replay bool, condition string, args ...any) error {
	run := &servermodels.AgentRun{}
	if err := db.NewSelect().Model(run).Column("agr.id", "agr.conversation_id").
		Where("agr.id = ? AND agr.workspace_id = ?", runID, workspaceID).Scan(ctx); err != nil {
		return fmt.Errorf("load agent run of lost computer operations: %w", err)
	}
	settled, requeued := 0, 0
	err := realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if _, err := chatstate.LockConversation(ctx, tx, workspaceID, run.ConversationID); err != nil {
			return err
		}
		settled, requeued = 0, 0
		active := false
		if replay {
			// 运行已结束时，电脑上执行中的调用除批准后执行的以外一律结算。
			var err error
			active, err = tx.NewSelect().Model((*servermodels.AgentRun)(nil)).
				Where("agr.id = ? AND agr.status IN (?)", runID, bun.List(domain.AgentRunActiveStatuses)).Exists(ctx)
			if err != nil {
				return fmt.Errorf("check agent run of lost computer operations: %w", err)
			}
		}
		calls := make([]servermodels.AgentToolCall, 0)
		if err := tx.NewRaw(`
			SELECT atc.* FROM agent_tool_calls AS atc
			JOIN computers AS cmp ON cmp.id = atc.computer_id
			WHERE atc.workspace_id = ? AND atc.agent_run_id = ? AND atc.status IN (?) AND `+condition+`
			FOR UPDATE OF atc
		`, append([]any{workspaceID, runID, bun.List(unsettledStatuses)}, args...)...).Scan(ctx, &calls); err != nil {
			return fmt.Errorf("lock lost computer operations: %w", err)
		}
		review := false
		lost := make([]servermodels.AgentToolCall, 0, len(calls))
		for _, call := range calls {
			claimed := call.Status == string(domain.AgentToolCallRunning)
			if replay && (active || agentprocess.Submitted(&call)) && claimed && call.Replayable && call.Claims < maxClaims {
				if _, err := tx.NewUpdate().Model(&call).
					Set("status = ?", domain.AgentToolCallQueued).
					Set("started_at = NULL").
					WherePK().Exec(ctx); err != nil {
					return fmt.Errorf("requeue lost computer operation: %w", err)
				}
				requeued++
				continue
			}
			// 委派本机 Agent 的轮次丢失时不按重新执行与外部副作用判断，已开始的记为中断。
			turn := call.LocalAgentSessionID != nil
			status, result, failure := agentcontract.ComputerLost(claimed, call.Replayable && !turn, call.SideEffects && !turn)
			if _, err := tx.NewUpdate().Model(&call).
				Set("status = ?", status).
				Set("result = ?", result).
				Set("error = ?", failure).
				Set("completed_at = now()").
				WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("settle lost computer operation: %w", err)
			}
			realtime.Notify(ctx, realtime.AgentToolCallSettled(workspaceID, call.ID))
			review = review || status == domain.AgentToolCallNeedsReview
			lost = append(lost, call)
		}
		settled = len(calls) - requeued
		if settled == 0 {
			return nil
		}
		return settler.OnLostToolCallsSettled(ctx, tx, workspaceID, runID, lost, review)
	})
	if err != nil {
		return err
	}
	if requeued > 0 {
		slog.InfoContext(logscope.WithWorkspace(ctx, workspaceID), "执行器已丢失电脑操作，已重新派发", "agent_run_id", runID, "count", requeued)
	}
	if settled > 0 {
		slog.InfoContext(logscope.WithWorkspace(ctx, workspaceID), "电脑操作已丢失，已结算", "agent_run_id", runID, "count", settled)
	}
	return nil
}
