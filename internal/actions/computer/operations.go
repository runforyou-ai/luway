//go:build server

package computer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// maxClaimBatch 是执行器一次领取的操作数上限。
const maxClaimBatch = 32

// OperationsAction 处理执行器领取操作与上报结果。
type OperationsAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewOperationsAction 创建操作领取与结果上报处理。
func NewOperationsAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *OperationsAction {
	return &OperationsAction{db: db, enqueuer: enqueuer}
}

// Claim 先结算电脑上执行中但执行器已不再持有的调用，找出执行器正在执行而所属运行已结束或调用已结算的操作交给执行器中止，
// 再按派发顺序领取待执行操作并标记为执行中；最多领取 limit 个，且执行中的操作总数不超过电脑的同时执行上限。running 是执行器本机仍在执行的操作编号。
func (a *OperationsAction) Claim(ctx context.Context, computer Identity, limit int, running []string) (ClaimResult, error) {
	// 执行器每次领取都带上本机仍在执行的操作，其余执行中的调用已随执行器重启或领取响应丢失而无人执行。
	held := make([]string, 0, len(running))
	for _, id := range running {
		if common.ValidUUID(id) {
			held = append(held, id)
		}
	}
	condition, args := "cmp.id = ? AND atc.status = ?", []any{computer.ComputerID, domain.AgentToolCallRunning}
	if len(held) > 0 {
		condition, args = condition+" AND atc.id NOT IN (?)", append(args, bun.In(held))
	}
	if err := settleMatchingCalls(ctx, a.db, a.enqueuer, condition, args...); err != nil {
		return ClaimResult{}, err
	}
	abort := make([]string, 0)
	if len(held) > 0 {
		// 仍需执行的操作：调用执行中且所属运行尚未结束。
		var active []string
		if err := a.db.NewSelect().Model((*servermodels.AgentToolCall)(nil)).
			ColumnExpr("atc.id::text").
			Join("JOIN agent_runs AS agr ON agr.organization_id = atc.organization_id AND agr.id = atc.agent_run_id").
			Where("atc.organization_id = ? AND atc.computer_id = ? AND atc.id IN (?)", computer.OrganizationID, computer.ComputerID, bun.In(held)).
			Where("atc.status = ? AND agr.status IN (?)", domain.AgentToolCallRunning, bun.In(domain.AgentRunActiveStatuses)).
			Scan(ctx, &active); err != nil {
			return ClaimResult{}, fmt.Errorf("list active computer operations: %w", err)
		}
		for _, id := range held {
			if !slices.Contains(active, id) {
				abort = append(abort, id)
			}
		}
	}
	limit = min(max(limit, 0), maxClaimBatch)
	if limit == 0 {
		return ClaimResult{Operations: []Operation{}, Abort: abort}, nil
	}
	rows := make([]servermodels.AgentToolCall, 0)
	if err := a.db.NewRaw(`
		UPDATE agent_tool_calls AS atc
		SET status = ?, started_at = now(), updated_at = now()
		FROM (
			SELECT pending.id FROM agent_tool_calls AS pending
			WHERE pending.organization_id = ? AND pending.computer_id = ? AND pending.status = ?
			ORDER BY pending.updated_at ASC, pending.id ASC
			LIMIT GREATEST(LEAST(?, (SELECT max_concurrency FROM computers WHERE id = ?) - (
				SELECT count(*) FROM agent_tool_calls WHERE computer_id = ? AND status = ?
			)), 0)
			FOR UPDATE OF pending SKIP LOCKED
		) AS picked
		WHERE atc.id = picked.id
		RETURNING atc.id, atc.operation
	`, domain.AgentToolCallRunning, computer.OrganizationID, computer.ComputerID, domain.AgentToolCallQueued,
		limit, computer.ComputerID, computer.ComputerID, domain.AgentToolCallRunning).Scan(ctx, &rows); err != nil {
		return ClaimResult{}, fmt.Errorf("claim computer operations: %w", err)
	}
	operations := make([]Operation, 0, len(rows))
	for _, row := range rows {
		if row.Operation == nil {
			continue
		}
		operations = append(operations, Operation{ID: row.ID, Operation: row.Operation.Operation})
	}
	if len(operations) > 0 {
		slog.Info("电脑已领取操作", "organization_id", computer.OrganizationID, "computer_id", computer.ComputerID, "count", len(operations))
	}
	if len(abort) > 0 {
		slog.Info("要求电脑中止操作", "organization_id", computer.OrganizationID, "computer_id", computer.ComputerID, "count", len(abort))
	}
	return ClaimResult{Operations: operations, Abort: abort}, nil
}

// Complete 记录电脑执行一次已领取操作的结果并唤醒等待该结果的运行：按中止要求提前结束的操作按可重新执行与外部副作用中断或待核对，
// 其余操作如实记录成功或失败；调用已结束或不属于该电脑执行中的操作时直接返回，重复上报保持幂等。
func (a *OperationsAction) Complete(ctx context.Context, computer Identity, callID string, outcome domain.ComputerOutcome) error {
	if !common.ValidUUID(callID) {
		return nil
	}
	initial := &servermodels.AgentToolCall{}
	err := a.db.NewSelect().Model(initial).
		Where("atc.id = ? AND atc.organization_id = ? AND atc.computer_id = ?", callID, computer.OrganizationID, computer.ComputerID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("load computer operation: %w", err)
	}
	run := &servermodels.AgentRun{}
	if err := a.db.NewSelect().Model(run).Column("agr.id", "agr.conversation_id").Where("agr.id = ?", initial.AgentRunID).Scan(ctx); err != nil {
		return fmt.Errorf("load computer operation run: %w", err)
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 按会话、调用的顺序加锁。
		if _, err := chatstate.LockConversation(ctx, tx, computer.OrganizationID, run.ConversationID); err != nil {
			return err
		}
		call := &servermodels.AgentToolCall{}
		if err := tx.NewSelect().Model(call).Where("atc.id = ?", callID).For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock computer operation: %w", err)
		}
		if call.Status != string(domain.AgentToolCallRunning) || call.Operation == nil {
			return nil
		}
		record := *call.Operation
		record.Outcome = &outcome
		update := tx.NewUpdate().Model(call).
			Set("operation = ?", record).
			Set("completed_at = now()").
			Set("updated_at = now()")
		switch {
		case outcome.Aborted:
			status, result := agentruntime.InterruptedStatus(call.Replayable, call.SideEffects)
			update = update.Set("status = ?", status).Set("result = ?", result)
		case outcome.Error != "":
			update = update.Set("status = ?", domain.AgentToolCallFailed).Set("error = ?", outcome.Error)
		default:
			update = update.Set("status = ?", domain.AgentToolCallSucceeded).Set("result = ?", outcome.Output)
		}
		if _, err := update.WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("complete computer operation: %w", err)
		}
		return agentrunaction.ResumeWaitingRun(ctx, tx, a.enqueuer, computer.OrganizationID, call.AgentRunID)
	})
}
