//go:build server

package computer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// SweepActionName 是结算离线电脑上未结束调用的定时 Action 名称。
const SweepActionName = "computer.sweep"

// unsettledStatuses 是派发到电脑后尚未结束的调用状态：待领取与执行中。
var unsettledStatuses = []domain.AgentToolCallStatus{domain.AgentToolCallWaiting, domain.AgentToolCallRunning}

// lostCondition 返回派发到电脑的未结束调用已丢失的判断条件及其参数：电脑已撤销；尚未领取而电脑离线；
// 已领取而电脑离线超过单次操作的执行时限，执行器在此之前重新上线时仍可上报结果。
func lostCondition() (string, []any) {
	return `(cmp.revoked_at IS NOT NULL
		OR (atc.status = ? AND (cmp.last_seen_at IS NULL OR cmp.last_seen_at < now() - make_interval(secs => ?)))
		OR (atc.status = ? AND (cmp.last_seen_at IS NULL OR cmp.last_seen_at < now() - make_interval(secs => ?))))`,
		[]any{domain.AgentToolCallWaiting, domain.ComputerPresenceTimeout.Seconds(),
			domain.AgentToolCallRunning, (domain.ComputerOperationTimeout + domain.ComputerPresenceTimeout).Seconds()}
}

// SweepAction 结算已撤销或离线电脑上未结束的调用。
type SweepAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewSweepAction 创建离线电脑调用结算。
func NewSweepAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *SweepAction {
	return &SweepAction{db: db, enqueuer: enqueuer}
}

// Execute 找出派发到已撤销或离线电脑且已丢失的调用，逐个运行结算并唤醒等待的运行。
func (a *SweepAction) Execute(ctx context.Context, _ struct{}) error {
	condition, args := lostCondition()
	return settleMatchingCalls(ctx, a.db, a.enqueuer, condition, args...)
}

// settleMatchingCalls 找出满足条件的派发到电脑且未结束的调用，按运行逐个结算并唤醒等待的运行；条件中电脑别名为 cmp、调用别名为 atc。
func settleMatchingCalls(ctx context.Context, db *bun.DB, enqueuer servertask.TxEnqueuer, condition string, args ...any) error {
	var runs []struct {
		OrganizationID string `bun:"organization_id"`
		AgentRunID     string `bun:"agent_run_id"`
	}
	if err := db.NewRaw(`
		SELECT DISTINCT atc.organization_id::text, atc.agent_run_id::text
		FROM agent_tool_calls AS atc
		JOIN computers AS cmp ON cmp.id = atc.computer_id
		WHERE atc.status IN (?) AND `+condition,
		append([]any{bun.In(unsettledStatuses)}, args...)...).Scan(ctx, &runs); err != nil {
		return fmt.Errorf("list lost computer operations: %w", err)
	}
	for _, run := range runs {
		if err := settleLostCalls(ctx, db, enqueuer, run.OrganizationID, run.AgentRunID, condition, args...); err != nil {
			return err
		}
	}
	return nil
}

// settleLostCalls 在运行所属会话的锁内结算该运行满足条件的派发到电脑且未结束的调用，并唤醒等待这些结果的运行：
// 尚未领取的调用失败，已领取的调用按可重新执行与外部副作用中断或待核对。
func settleLostCalls(ctx context.Context, db *bun.DB, enqueuer servertask.TxEnqueuer, organizationID, runID, condition string, args ...any) error {
	run := &servermodels.AgentRun{}
	if err := db.NewSelect().Model(run).Column("agr.id", "agr.conversation_id").
		Where("agr.id = ? AND agr.organization_id = ?", runID, organizationID).Scan(ctx); err != nil {
		return fmt.Errorf("load agent run of lost computer operations: %w", err)
	}
	settled := 0
	err := realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if _, err := chatstate.LockConversation(ctx, tx, organizationID, run.ConversationID); err != nil {
			return err
		}
		calls := make([]servermodels.AgentToolCall, 0)
		if err := tx.NewRaw(`
			SELECT atc.* FROM agent_tool_calls AS atc
			JOIN computers AS cmp ON cmp.id = atc.computer_id
			WHERE atc.organization_id = ? AND atc.agent_run_id = ? AND atc.status IN (?) AND `+condition+`
			FOR UPDATE OF atc
		`, append([]any{organizationID, runID, bun.In(unsettledStatuses)}, args...)...).Scan(ctx, &calls); err != nil {
			return fmt.Errorf("lock lost computer operations: %w", err)
		}
		now := time.Now()
		for _, call := range calls {
			status, result, failure := agentruntime.ComputerLost(call.Status == string(domain.AgentToolCallRunning), call.Replayable, call.SideEffects)
			if _, err := tx.NewUpdate().Model(&call).
				Set("status = ?", status).
				Set("result = ?", result).
				Set("error = ?", failure).
				Set("completed_at = ?", now).
				Set("updated_at = now()").
				WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("settle lost computer operation: %w", err)
			}
		}
		settled = len(calls)
		if settled == 0 {
			return nil
		}
		return agentrunaction.ResumeWaitingRun(ctx, tx, enqueuer, organizationID, runID)
	})
	if err != nil {
		return err
	}
	if settled > 0 {
		slog.Info("电脑不可用，已结算派发给它的调用", "organization_id", organizationID, "agent_run_id", runID, "count", settled)
	}
	return nil
}
