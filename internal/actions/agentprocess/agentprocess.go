//go:build server

// Package agentprocess 读写 Agent 运行的过程内容：有序内容块与工具调用记录。
package agentprocess

import (
	"context"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// settledStatuses 是已有最终结果的工具调用状态，写入时保持原值。
var settledStatuses = []domain.AgentToolCallStatus{
	domain.AgentToolCallSucceeded, domain.AgentToolCallFailed, domain.AgentToolCallCancelled,
	domain.AgentToolCallInterrupted, domain.AgentToolCallNeedsReview,
}

// Load 按位置顺序读取运行的内容块，工具调用块带上对应的工具调用记录，并返回子 Agent 发起的工具调用。
func Load(ctx context.Context, db bun.IDB, organizationID, runID string) ([]agentruntime.Block, []agentruntime.ToolCall, error) {
	var rows []servermodels.AgentRunBlock
	if err := db.NewSelect().Model(&rows).
		Where("arb.organization_id = ? AND arb.agent_run_id = ?", organizationID, runID).
		OrderExpr("arb.position").Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("load agent run blocks: %w", err)
	}
	var calls []servermodels.AgentToolCall
	if err := db.NewSelect().Model(&calls).
		Where("atc.organization_id = ? AND atc.agent_run_id = ?", organizationID, runID).
		OrderExpr("atc.id").Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("load agent tool calls: %w", err)
	}
	byID := make(map[string]agentruntime.ToolCall, len(calls))
	children := make([]agentruntime.ToolCall, 0)
	for _, call := range calls {
		converted := ToolCall(call)
		byID[call.ID] = converted
		if call.ParentID != nil {
			children = append(children, converted)
		}
	}
	blocks := make([]agentruntime.Block, 0, len(rows))
	for _, row := range rows {
		block := agentruntime.Block{ID: row.ID, Position: row.Position, ModelCallID: row.ModelCallID, Kind: domain.AgentRunBlockKind(row.Kind)}
		if row.Content != nil {
			block.Payload.Text = *row.Content
		}
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
func ToolCall(row servermodels.AgentToolCall) agentruntime.ToolCall {
	call := agentruntime.ToolCall{
		ID: row.ID, ModelCallID: row.ModelCallID, CallID: row.ProviderCallID, Name: row.Name, Source: domain.AgentToolSource(row.Source),
		Replayable: row.Replayable, SideEffects: row.SideEffects, Arguments: row.Arguments, Result: row.Result, Error: row.Error,
		Status: domain.AgentToolCallStatus(row.Status), StartedAt: row.StartedAt, CompletedAt: row.CompletedAt, Evidence: row.Evidence,
	}
	if row.ParentID != nil {
		call.ParentID = *row.ParentID
	}
	if row.MCPServer != nil {
		call.MCPServer = *row.MCPServer
	}
	return call
}

// Sync 以给定的内容块与子 Agent 调用替换运行已保存的过程：删除给定集合以外的内容块与工具调用，工具调用按 SaveToolCall 的规则写入。
func Sync(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, blocks []agentruntime.Block, children []agentruntime.ToolCall) error {
	calls := slices.Clone(children)
	rows := make([]servermodels.AgentRunBlock, 0, len(blocks))
	for _, block := range blocks {
		row := servermodels.AgentRunBlock{ID: block.ID, OrganizationID: run.OrganizationID, AgentRunID: run.ID,
			Position: block.Position, ModelCallID: block.ModelCallID, Kind: string(block.Kind)}
		if call := block.Payload.ToolCall; call != nil {
			row.ToolCallID = &call.ID
			calls = append(calls, *call)
		} else {
			text := block.Payload.Text
			row.Content = &text
		}
		rows = append(rows, row)
	}
	callIDs := make([]string, 0, len(calls))
	for _, call := range calls {
		callIDs = append(callIDs, call.ID)
	}
	blockIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		blockIDs = append(blockIDs, row.ID)
	}
	deleteBlocks := db.NewDelete().Model((*servermodels.AgentRunBlock)(nil)).
		Where("organization_id = ? AND agent_run_id = ?", run.OrganizationID, run.ID)
	if len(blockIDs) > 0 {
		deleteBlocks = deleteBlocks.Where("id NOT IN (?)", bun.In(blockIDs))
	}
	if _, err := deleteBlocks.Exec(ctx); err != nil {
		return fmt.Errorf("delete stale agent run blocks: %w", err)
	}
	deleteCalls := db.NewDelete().Model((*servermodels.AgentToolCall)(nil)).
		Where("organization_id = ? AND agent_run_id = ?", run.OrganizationID, run.ID)
	if len(callIDs) > 0 {
		deleteCalls = deleteCalls.Where("id NOT IN (?)", bun.In(callIDs))
	}
	if _, err := deleteCalls.Exec(ctx); err != nil {
		return fmt.Errorf("delete stale agent tool calls: %w", err)
	}
	for _, call := range calls {
		if err := SaveToolCall(ctx, db, run, call); err != nil {
			return err
		}
	}
	if len(rows) == 0 {
		return nil
	}
	// 位置在本次写入中可能整体前移，先腾开位置再写入，避开位置唯一约束。
	if _, err := db.NewUpdate().Model((*servermodels.AgentRunBlock)(nil)).
		Set("position = -position").
		Where("organization_id = ? AND agent_run_id = ?", run.OrganizationID, run.ID).Exec(ctx); err != nil {
		return fmt.Errorf("release agent run block positions: %w", err)
	}
	if _, err := db.NewInsert().Model(&rows).
		On("CONFLICT (id) DO UPDATE").
		Set("position = EXCLUDED.position").
		Set("model_call_id = EXCLUDED.model_call_id").
		Set("kind = EXCLUDED.kind").
		Set("content = EXCLUDED.content").
		Set("tool_call_id = EXCLUDED.tool_call_id").
		Set("updated_at = now()").
		Exec(ctx); err != nil {
		return fmt.Errorf("save agent run blocks: %w", err)
	}
	return nil
}

// SaveToolCall 写入一次工具调用：已有最终结果的调用只补记依据标记，其余字段保持不变。
func SaveToolCall(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, call agentruntime.ToolCall) error {
	row := &servermodels.AgentToolCall{
		ID: call.ID, OrganizationID: run.OrganizationID, AgentRunID: run.ID, ModelCallID: call.ModelCallID,
		ProviderCallID: call.CallID, Name: call.Name, Source: string(call.Source), Arguments: call.Arguments,
		Replayable: call.Replayable, SideEffects: call.SideEffects, Status: string(call.Status),
		Result: call.Result, Error: call.Error, Evidence: call.Evidence, StartedAt: call.StartedAt, CompletedAt: call.CompletedAt,
	}
	if call.ParentID != "" {
		row.ParentID = &call.ParentID
	}
	if call.MCPServer != "" {
		row.MCPServer = &call.MCPServer
	}
	settled := bun.In(settledStatuses)
	if _, err := db.NewInsert().Model(row).
		On("CONFLICT (id) DO UPDATE").
		Set("arguments = EXCLUDED.arguments").
		Set("status = CASE WHEN atc.status IN (?) THEN atc.status ELSE EXCLUDED.status END", settled).
		Set("result = CASE WHEN atc.status IN (?) THEN atc.result ELSE EXCLUDED.result END", settled).
		Set("error = CASE WHEN atc.status IN (?) THEN atc.error ELSE EXCLUDED.error END", settled).
		Set("completed_at = CASE WHEN atc.status IN (?) THEN atc.completed_at ELSE EXCLUDED.completed_at END", settled).
		Set("started_at = COALESCE(atc.started_at, EXCLUDED.started_at)").
		Set("evidence = atc.evidence OR EXCLUDED.evidence").
		Set("updated_at = now()").
		Exec(ctx); err != nil {
		return fmt.Errorf("save agent tool call: %w", err)
	}
	return nil
}

// CancelUnsettled 把运行中尚未结束的工具调用记为已取消，运行以失败或取消结束时在同一事务中调用。
func CancelUnsettled(ctx context.Context, db bun.IDB, organizationID string, runIDs ...string) error {
	if len(runIDs) == 0 {
		return nil
	}
	if _, err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("status = ?", domain.AgentToolCallCancelled).
		Set("completed_at = now()").
		Set("updated_at = now()").
		Where("organization_id = ? AND agent_run_id IN (?)", organizationID, bun.In(runIDs)).
		Where("status IN (?)", bun.In([]domain.AgentToolCallStatus{domain.AgentToolCallQueued, domain.AgentToolCallRunning, domain.AgentToolCallWaiting})).
		Exec(ctx); err != nil {
		return fmt.Errorf("cancel unsettled agent tool calls: %w", err)
	}
	return nil
}
