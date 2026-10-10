//go:build server

// Package agentprocess 读写 Agent 运行的过程内容：有序内容块与工具调用记录。
package agentprocess

import (
	"context"
	"fmt"
	"slices"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// Load 按位置顺序读取运行的内容块，工具调用块带上对应的工具调用记录，并返回子 Agent 发起的工具调用。
func Load(ctx context.Context, db bun.IDB, workspaceID, runID string) ([]agentcontract.Block, []agentcontract.ToolCall, error) {
	var rows []servermodels.AgentRunBlock
	if err := db.NewSelect().Model(&rows).
		Where("arb.workspace_id = ? AND arb.agent_run_id = ?", workspaceID, runID).
		OrderExpr("arb.position").Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("load agent run blocks: %w", err)
	}
	// 本机 Agent 的权限请求由本机 Agent 会话推进，不属于运行的过程。
	var calls []servermodels.AgentToolCall
	if err := db.NewSelect().Model(&calls).
		Where("atc.workspace_id = ? AND atc.agent_run_id = ?", workspaceID, runID).
		Where("atc.source <> ?", domain.AgentToolSourceLocalAgent).
		OrderExpr("atc.id").Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("load agent tool calls: %w", err)
	}
	byID := make(map[string]agentcontract.ToolCall, len(calls))
	children := make([]agentcontract.ToolCall, 0)
	for _, call := range calls {
		converted := ToolCall(call)
		byID[call.ID] = converted
		if call.ParentID != nil {
			children = append(children, converted)
		}
	}
	blocks := make([]agentcontract.Block, 0, len(rows))
	for _, row := range rows {
		block := agentcontract.Block{ID: row.ID, Position: row.Position, ModelCallID: row.ModelCallID, Kind: domain.AgentRunBlockKind(row.Kind)}
		block.Payload.Text = support.Deref(row.Content)
		if row.ToolCallID != nil {
			call, ok := byID[*row.ToolCallID]
			if !ok {
				return nil, nil, fmt.Errorf("agent run block %s references a missing tool call", row.ID)
			}
			block.Payload.ToolCall = &call
		}
		blocks = append(blocks, block)
	}
	return blocks, children, nil
}

// ToolCall 把工具调用记录转换为运行时的工具调用。
func ToolCall(row servermodels.AgentToolCall) agentcontract.ToolCall {
	call := agentcontract.ToolCall{
		ID: row.ID, ModelCallID: row.ModelCallID, CallID: row.ProviderCallID, Name: row.Name, Source: domain.AgentToolSource(row.Source),
		Replayable: row.Replayable, SideEffects: row.SideEffects, Arguments: row.Arguments, BoundArguments: row.BoundArguments, Result: row.Result, Error: row.Error,
		Status: domain.AgentToolCallStatus(row.Status), StartedAt: row.StartedAt, CompletedAt: row.CompletedAt, Evidence: row.Evidence,
		Computer: row.ComputerID != nil,
	}
	if row.Operation != nil {
		call.ComputerOutcome = row.Operation.Outcome
		if row.LocalAgentSessionID != nil {
			call.LocalAgent = row.Operation.Operation.LocalAgent
		}
	}
	call.ParentID = support.Deref(row.ParentID)
	call.MCPServer = support.Deref(row.MCPServer)
	if row.BusinessSystemID != nil && row.BusinessSystemName != nil {
		call.BusinessSystemID, call.BusinessSystem = *row.BusinessSystemID, *row.BusinessSystemName
	}
	if row.Level != nil {
		call.Level = domain.OperationLevel(*row.Level)
	}
	if row.Intervention != nil {
		call.Intervention = domain.ToolIntervention(*row.Intervention)
	}
	return call
}

// upsertBlocks 以一条语句写入内容块记录，已有记录更新位置、所属模型调用、类型、文本与工具调用编号。
func upsertBlocks(ctx context.Context, db bun.IDB, rows []servermodels.AgentRunBlock) error {
	if _, err := db.NewInsert().Model(&rows).
		On("CONFLICT (id) DO UPDATE").
		Set("position = EXCLUDED.position").
		Set("model_call_id = EXCLUDED.model_call_id").
		Set("kind = EXCLUDED.kind").
		Set("content = EXCLUDED.content").
		Set("tool_call_id = EXCLUDED.tool_call_id").
		Exec(ctx); err != nil {
		return fmt.Errorf("save agent run blocks: %w", err)
	}
	return nil
}

// SettleEndedRuns 在运行以失败或取消结束的事务中结算这些运行尚未结束的工具调用，并通知正在执行这些运行的服务端实例与相关电脑：
// 尚未开始与等待外部结果的调用、暂停运行等待确认的调用取消；服务端进程内已开始的调用按可重新执行与外部副作用中断或待核对；已提交确认或审批的调用与交给本机 Agent 会话的轮次不受运行结束影响；
// 电脑已领取的调用保持执行中，电脑领取时收到中止要求，中止后上报结果。调用方必须处于 realtime.RunInTx 内。
func SettleEndedRuns(ctx context.Context, db bun.IDB, workspaceID string, runIDs ...string) error {
	if len(runIDs) == 0 {
		return nil
	}
	if _, err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("status = ?", domain.AgentToolCallCancelled).
		Set("completed_at = now()").
		Where("workspace_id = ? AND agent_run_id IN (?) AND handover IS DISTINCT FROM ? AND local_agent_session_id IS NULL", workspaceID, bun.List(runIDs), einorun.HandoverSubmitted).
		Where("status IN (?)", bun.List([]domain.AgentToolCallStatus{domain.AgentToolCallQueued, domain.AgentToolCallWaiting})).
		Exec(ctx); err != nil {
		return fmt.Errorf("cancel pending agent tool calls: %w", err)
	}
	// 暂停运行等待确认的调用随运行结束取消，通知处理人刷新待处理操作。
	var paused []struct {
		AssigneeSubjectID *string `bun:"assignee_subject_id"`
	}
	if err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("status = ?", domain.AgentToolCallCancelled).
		Set("decided_at = COALESCE(decided_at, now())").
		Set("completed_at = now()").
		Where("workspace_id = ? AND agent_run_id IN (?) AND handover IS NULL AND status = ? AND source <> ?", workspaceID, bun.List(runIDs), domain.AgentToolCallAwaitingDecision, domain.AgentToolSourceLocalAgent).
		Returning("assignee_subject_id").
		Scan(ctx, &paused); err != nil {
		return fmt.Errorf("cancel paused agent tool calls: %w", err)
	}
	subjects := make([]string, 0, len(paused))
	for _, call := range paused {
		if call.AssigneeSubjectID != nil {
			subjects = append(subjects, *call.AssigneeSubjectID)
		}
	}
	if err := NotifyDecisionSubjects(ctx, db, workspaceID, subjects...); err != nil {
		return err
	}
	var running []servermodels.AgentToolCall
	if err := db.NewSelect().Model(&running).
		Where("atc.workspace_id = ? AND atc.agent_run_id IN (?) AND atc.status = ? AND atc.handover IS DISTINCT FROM ?", workspaceID, bun.List(runIDs), domain.AgentToolCallRunning, einorun.HandoverSubmitted).
		OrderExpr("atc.id").For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("lock running agent tool calls: %w", err)
	}
	computerIDs := make([]string, 0)
	reviewRunIDs := make([]string, 0)
	for _, call := range running {
		if call.ComputerID != nil {
			if !slices.Contains(computerIDs, *call.ComputerID) {
				computerIDs = append(computerIDs, *call.ComputerID)
			}
			continue
		}
		status, result := agentcontract.InterruptedStatus(call.Replayable, call.SideEffects)
		if _, err := db.NewUpdate().Model(&call).
			Set("status = ?", status).
			Set("result = ?", result).
			Set("completed_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("interrupt running agent tool call: %w", err)
		}
		if status == domain.AgentToolCallNeedsReview {
			reviewRunIDs = append(reviewRunIDs, call.AgentRunID)
		}
	}
	if err := NotifyReviewers(ctx, db, workspaceID, reviewRunIDs...); err != nil {
		return err
	}
	for _, computerID := range computerIDs {
		realtime.Notify(ctx, realtime.ComputerWork(workspaceID, computerID))
	}
	for _, runID := range runIDs {
		realtime.Notify(ctx, realtime.AgentRunEnded(workspaceID, runID))
	}
	return nil
}
