//go:build server

package computer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

const (
	// maxClaimBatch 是执行器一次领取的操作数上限。
	maxClaimBatch = 32
	// maxClaims 是一次调用最多被电脑领取的次数，执行器丢失的可重新执行调用在此之内重新派发。
	maxClaims = 3
)

// OperationsAction 处理执行器领取操作与上报结果。
type OperationsAction struct {
	db      *bun.DB
	settler ToolCallSettler
}

// NewOperationsAction 创建操作领取与结果上报处理，settler 在调用有结果或请求权限时推进工具决定与等待的运行。
func NewOperationsAction(db *bun.DB, settler ToolCallSettler) *OperationsAction {
	return &OperationsAction{db: db, settler: settler}
}

// Claim 先处理电脑上执行中但执行器已不再持有的调用：可重新执行的调用重新派发，其余结算；再找出执行器正在执行而应当中止的操作、执行器持有而已释放的本机 Agent 会话与已有结果的权限请求，最后按派发顺序领取待执行操作并标记为执行中：电脑操作最多领取 limit 个，且执行中的电脑操作总数不超过电脑的同时执行上限；委派本机 Agent 的轮次不占同时执行上限，每个会话同一时刻只执行一轮。
func (a *OperationsAction) Claim(ctx context.Context, computer Identity, input ClaimInput) (ClaimResult, error) {
	// 执行器每次领取都带上本机仍在执行的操作，其余执行中的调用已随执行器重启或领取响应丢失而无人执行。
	held := arr.Filter(input.Running, str.IsUUID)
	condition, args := "cmp.id = ? AND atc.status = ?", []any{computer.ComputerID, domain.AgentToolCallRunning}
	if len(held) > 0 {
		condition, args = condition+" AND atc.id NOT IN (?)", append(args, bun.List(held))
	}
	if err := settleMatchingCalls(ctx, a.db, a.settler, true, condition, args...); err != nil {
		return ClaimResult{}, err
	}
	result := ClaimResult{Operations: []Operation{}, Abort: []string{}, Released: []string{}, Permissions: []PermissionResult{}}
	if len(held) > 0 {
		// 仍需执行的操作：调用执行中，委派本机 Agent 的轮次所属会话未释放，其余调用是提交后批准执行的调用或所属运行尚未结束。
		var active []string
		if err := a.db.NewSelect().Model((*servermodels.AgentToolCall)(nil)).
			ColumnExpr("atc.id::text").
			Join("JOIN agent_runs AS agr ON agr.workspace_id = atc.workspace_id AND agr.id = atc.agent_run_id").
			Join("LEFT JOIN local_agent_sessions AS las ON las.workspace_id = atc.workspace_id AND las.id = atc.local_agent_session_id").
			Where("atc.workspace_id = ? AND atc.computer_id = ? AND atc.id IN (?) AND atc.status = ?", computer.WorkspaceID, computer.ComputerID, bun.List(held), domain.AgentToolCallRunning).
			Where("CASE WHEN las.id IS NOT NULL THEN las.status = ? ELSE atc.handover = ? OR agr.status IN (?) END",
				domain.LocalAgentSessionActive, einorun.HandoverSubmitted, bun.List(domain.AgentRunActiveStatuses)).
			Scan(ctx, &active); err != nil {
			return ClaimResult{}, fmt.Errorf("list active computer operations: %w", err)
		}
		result.Abort = append(result.Abort, arr.Diff(held, active)...)
	}
	if sessions := arr.Filter(input.Sessions, str.IsUUID); len(sessions) > 0 {
		var active []string
		if err := a.db.NewSelect().Model((*servermodels.LocalAgentSession)(nil)).
			ColumnExpr("las.id::text").
			Where("las.workspace_id = ? AND las.computer_id = ? AND las.id IN (?) AND las.status = ?", computer.WorkspaceID, computer.ComputerID, bun.List(sessions), domain.LocalAgentSessionActive).
			Scan(ctx, &active); err != nil {
			return ClaimResult{}, fmt.Errorf("list active local agent sessions: %w", err)
		}
		for _, id := range sessions {
			if !slices.Contains(active, id) {
				result.Released = append(result.Released, id)
			}
		}
	}
	if permissions := arr.Filter(input.Permissions, str.IsUUID); len(permissions) > 0 {
		// 仍在等待裁决的请求不返回，找不到的请求按取消返回。
		var decided []permissionRow
		if err := a.db.NewSelect().Model((*servermodels.AgentToolCall)(nil)).
			ColumnExpr("atc.id::text, atc.status, atc.result").
			Where("atc.workspace_id = ? AND atc.computer_id = ? AND atc.id IN (?) AND atc.source = ?", computer.WorkspaceID, computer.ComputerID, bun.List(permissions), domain.AgentToolSourceLocalAgent).
			Scan(ctx, &decided); err != nil {
			return ClaimResult{}, fmt.Errorf("list local agent permission results: %w", err)
		}
		for _, id := range permissions {
			index := slices.IndexFunc(decided, func(row permissionRow) bool { return row.ID == id })
			switch {
			case index < 0:
				result.Permissions = append(result.Permissions, PermissionResult{ID: id})
			case decided[index].Status == string(domain.AgentToolCallAwaitingDecision):
			case decided[index].Status == string(domain.AgentToolCallSucceeded) || decided[index].Status == string(domain.AgentToolCallRejected):
				result.Permissions = append(result.Permissions, PermissionResult{ID: id, OptionID: support.Deref(decided[index].Result)})
			default:
				result.Permissions = append(result.Permissions, PermissionResult{ID: id})
			}
		}
	}
	limit := min(max(input.Limit, 0), maxClaimBatch)
	rows := make([]servermodels.AgentToolCall, 0)
	if err := a.db.NewRaw(`
		WITH primitive AS (
			SELECT pending.id FROM agent_tool_calls AS pending
			WHERE pending.workspace_id = ? AND pending.computer_id = ? AND pending.status = ? AND pending.local_agent_session_id IS NULL
			ORDER BY pending.updated_at ASC, pending.id ASC
			LIMIT GREATEST(LEAST(?, (SELECT max_concurrency FROM computers WHERE id = ?) - (
				SELECT count(*) FROM agent_tool_calls WHERE computer_id = ? AND status = ? AND local_agent_session_id IS NULL
			)), 0)
			FOR UPDATE OF pending SKIP LOCKED
		), turn AS (
			SELECT DISTINCT ON (queued.local_agent_session_id) queued.id FROM agent_tool_calls AS queued
			WHERE queued.workspace_id = ? AND queued.computer_id = ? AND queued.status = ? AND queued.local_agent_session_id IS NOT NULL
				AND NOT EXISTS (SELECT 1 FROM agent_tool_calls AS current
					WHERE current.local_agent_session_id = queued.local_agent_session_id AND current.status = ?)
			ORDER BY queued.local_agent_session_id, queued.updated_at ASC, queued.id ASC
			LIMIT ?
		)
		UPDATE agent_tool_calls AS atc
		SET status = ?, claims = atc.claims + 1, started_at = now()
		FROM (SELECT id FROM primitive UNION ALL SELECT id FROM turn) AS picked
		WHERE atc.id = picked.id AND atc.status = ?
		RETURNING atc.id, atc.operation, atc.local_agent_session_id, atc.trace_id
	`, computer.WorkspaceID, computer.ComputerID, domain.AgentToolCallQueued, limit, computer.ComputerID, computer.ComputerID, domain.AgentToolCallRunning,
		computer.WorkspaceID, computer.ComputerID, domain.AgentToolCallQueued, domain.AgentToolCallRunning, maxClaimBatch,
		domain.AgentToolCallRunning, domain.AgentToolCallQueued,
	).Scan(ctx, &rows); err != nil {
		return ClaimResult{}, fmt.Errorf("claim computer operations: %w", err)
	}
	// 委派本机 Agent 的轮次带上会话中最近记录的 ACP 会话编号，排队期间前一轮完成时记录的编号同样生效。
	sessionIDs := arr.FilterMap(rows, func(row servermodels.AgentToolCall) (string, bool) {
		return support.Deref(row.LocalAgentSessionID), row.LocalAgentSessionID != nil
	})
	agentSessions := map[string]string{}
	if len(sessionIDs) > 0 {
		var sessions []servermodels.LocalAgentSession
		if err := a.db.NewSelect().Model(&sessions).Column("las.id", "las.session_id").
			Where("las.workspace_id = ? AND las.id IN (?)", computer.WorkspaceID, bun.List(sessionIDs)).Scan(ctx); err != nil {
			return ClaimResult{}, fmt.Errorf("load local agent sessions of claimed turns: %w", err)
		}
		for _, session := range sessions {
			agentSessions[session.ID] = support.Deref(session.SessionID)
		}
	}
	for _, row := range rows {
		if row.Operation == nil {
			continue
		}
		operation := Operation{ID: row.ID, Operation: row.Operation.Operation, Timeout: domain.ComputerOperationTimeout, TraceID: support.Deref(row.TraceID)}
		if row.LocalAgentSessionID != nil {
			operation.Timeout, operation.Operation.AgentSession = 0, agentSessions[*row.LocalAgentSessionID]
		}
		result.Operations = append(result.Operations, operation)
	}
	if len(result.Operations) > 0 {
		slog.InfoContext(logscope.WithWorkspace(ctx, computer.WorkspaceID), "电脑已领取操作", "computer_id", computer.ComputerID, "count", len(result.Operations))
	}
	if len(result.Abort) > 0 {
		slog.InfoContext(logscope.WithWorkspace(ctx, computer.WorkspaceID), "要求电脑中止操作", "computer_id", computer.ComputerID, "count", len(result.Abort))
	}
	return result, nil
}

// permissionRow 是一个权限请求的当前状态与选用的处理方式编号。
type permissionRow struct {
	ID     string  `bun:"id"`
	Status string  `bun:"status"`
	Result *string `bun:"result"`
}

// Complete 记录电脑执行一次已领取操作的结果：执行中的调用按中止要求提前结束时按可重新执行与外部副作用中断或待核对，委派本机 Agent 的轮次记为中断，其余如实记录成功或失败，并唤醒等待该结果的运行，批准后执行的调用以结果事件唤醒提交它的 AI 员工，委派本机 Agent 的轮次把结果写入会话；已中断或待核对的调用结果未知，以执行器补报的成功或失败补记实际结果；其余已结束或不属于该电脑的调用直接返回，重复上报保持幂等。
func (a *OperationsAction) Complete(ctx context.Context, computer Identity, callID string, outcome domain.ComputerOutcome) error {
	if !str.IsUUID(callID) {
		return nil
	}
	outcome.Output, outcome.Error = str.Remove(outcome.Output, "\x00"), str.Remove(outcome.Error, "\x00")
	initial := &servermodels.AgentToolCall{}
	err := a.db.NewSelect().Model(initial).
		Where("atc.id = ? AND atc.workspace_id = ? AND atc.computer_id = ?", callID, computer.WorkspaceID, computer.ComputerID).
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
		conversation, err := chatstate.LockConversation(ctx, tx, computer.WorkspaceID, run.ConversationID)
		if err != nil {
			return err
		}
		call := &servermodels.AgentToolCall{}
		if err := tx.NewSelect().Model(call).Where("atc.id = ?", callID).For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock computer operation: %w", err)
		}
		previous := domain.AgentToolCallStatus(call.Status)
		running := previous == domain.AgentToolCallRunning
		turn := call.LocalAgentSessionID != nil
		// 已中断或待核对的调用结果未知，已中止的补报仍不能确定实际结果，调用保持不变；委派本机 Agent 的轮次结束后不补记结果。
		unknown := (previous == domain.AgentToolCallInterrupted || previous == domain.AgentToolCallNeedsReview) && !outcome.Aborted && !turn
		if call.Operation == nil || (!running && !unknown) {
			return nil
		}
		record := *call.Operation
		record.Outcome = &outcome
		update := tx.NewUpdate().Model(call).
			Set("operation = ?", record)
		if running {
			update = update.Set("completed_at = now()")
		}
		// 调用进入或离开待核对时通知负责人刷新待处理。
		review := previous == domain.AgentToolCallNeedsReview
		switch {
		case outcome.Aborted && turn:
			status, result := agentcontract.InterruptedStatus(false, false)
			update = update.Set("status = ?", status).Set("result = ?", result)
		case outcome.Aborted:
			status, result := agentcontract.InterruptedStatus(call.Replayable, call.SideEffects)
			update = update.Set("status = ?", status).Set("result = ?", result)
			review = review || status == domain.AgentToolCallNeedsReview
		case outcome.Error != "":
			update = update.Set("status = ?", domain.AgentToolCallFailed).Set("result = NULL").Set("error = ?", outcome.Error)
		default:
			update = update.Set("status = ?", domain.AgentToolCallSucceeded).Set("result = ?", outcome.Output)
		}
		if _, err := update.WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("complete computer operation: %w", err)
		}
		realtime.Notify(ctx, realtime.AgentToolCallSettled(computer.WorkspaceID, callID))
		// 委派本机 Agent 的轮次以 AI 员工身份把结果写入会话，批准后执行的调用以结果事件唤醒 AI 员工，其余调用唤醒等待结果的运行；结果未知的调用只补记结果。
		if err := a.settler.OnToolCallSettled(ctx, tx, call, review, unknown); err != nil {
			return err
		}
		if unknown {
			slog.InfoContext(logscope.WithWorkspace(ctx, computer.WorkspaceID), "电脑补报了结果未知调用的执行结果", "tool_call_id", callID, "previous_status", previous, "failed", outcome.Error != "")
			return chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeTimeline)
		}
		return nil
	})
}

// ReportUpdates 记录电脑执行中的调用按序号上报的过程更新：调用须派发到该电脑且仍在执行，重复上报的序号保持首次写入；写入后通知会话成员读取新的过程。
func (a *OperationsAction) ReportUpdates(ctx context.Context, computer Identity, callID string, updates []UpdateInput) error {
	if !str.IsUUID(callID) || len(updates) == 0 {
		return nil
	}
	var conversationID string
	err := a.db.NewSelect().Model((*servermodels.AgentToolCall)(nil)).
		ColumnExpr("agr.conversation_id::text").
		Join("JOIN agent_runs AS agr ON agr.workspace_id = atc.workspace_id AND agr.id = atc.agent_run_id").
		Where("atc.workspace_id = ? AND atc.id = ? AND atc.computer_id = ? AND atc.status = ?", computer.WorkspaceID, callID, computer.ComputerID, domain.AgentToolCallRunning).
		Scan(ctx, &conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("load reporting computer operation: %w", err)
	}
	rows := arr.FilterMap(updates, func(update UpdateInput) (servermodels.AgentToolCallUpdate, bool) {
		return servermodels.AgentToolCallUpdate{ToolCallID: callID, Seq: update.Seq, WorkspaceID: computer.WorkspaceID, Content: update.Update}, update.Seq > 0
	})
	if len(rows) == 0 {
		return nil
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(&rows).On("CONFLICT (tool_call_id, seq) DO NOTHING").Exec(ctx); err != nil {
			return fmt.Errorf("save computer operation updates: %w", err)
		}
		conversation := &servermodels.Conversation{}
		if err := tx.NewSelect().Model(conversation).Column("id", "workspace_id", "type", "version").
			Where("cv.workspace_id = ? AND cv.id = ?", computer.WorkspaceID, conversationID).Scan(ctx); err != nil {
			return fmt.Errorf("load computer operation conversation: %w", err)
		}
		return chatstate.NotifyConversationChanged(ctx, tx, conversation, domain.ConversationChangeProcess)
	})
}

// RequestPermission 为执行中的委派本机 Agent 的一轮记录本机 Agent 的权限请求，交给这一轮的发起人确认。
func (a *OperationsAction) RequestPermission(ctx context.Context, computer Identity, callID string, input PermissionInput) error {
	return a.settler.RequestLocalAgentPermission(ctx, computer.WorkspaceID, computer.ComputerID, callID, input.ID, input.Permission)
}
