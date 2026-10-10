//go:build server

package tooldecision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// RequestLocalAgentPermission 在执行器上报权限请求的事务中为执行中的一轮新建等待发起人确认的权限请求：编号由执行器分配，重复上报保持幂等；
// 这一轮已不在执行或会话已释放时不新建，找不到发起人时请求直接取消，执行器按拒绝处理。
func (a *Action) RequestLocalAgentPermission(ctx context.Context, workspaceID, computerID, turnID, requestID string, permission domain.LocalAgentPermission) error {
	if !str.IsUUID(turnID) || !str.IsUUID(requestID) {
		return nil
	}
	arguments, err := json.Marshal(permission)
	if err != nil {
		return fmt.Errorf("encode local agent permission: %w", err)
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := a.lockToolCall(ctx, tx, workspaceID, turnID)
		if errors.Is(err, ErrToolCallUnavailable) {
			return nil
		}
		if err != nil {
			return err
		}
		turn := locked.Call
		if turn.ComputerID == nil || *turn.ComputerID != computerID || turn.LocalAgentSessionID == nil || turn.Status != string(domain.AgentToolCallRunning) {
			return nil
		}
		active, err := tx.NewSelect().Model((*servermodels.LocalAgentSession)(nil)).
			Where("las.id = ? AND las.status = ?", *turn.LocalAgentSessionID, domain.LocalAgentSessionActive).Exists(ctx)
		if err != nil || !active {
			return err
		}
		intervention := string(domain.ToolInterventionConfirmation)
		request := &servermodels.AgentToolCall{
			ID: requestID, WorkspaceID: workspaceID, AgentRunID: turn.AgentRunID, ParentID: &turn.ID, ModelCallID: turn.ModelCallID,
			ProviderCallID: requestID, Name: permission.Step.Title, Source: string(domain.AgentToolSourceLocalAgent), Arguments: string(arguments),
			Status: string(domain.AgentToolCallAwaitingDecision), Intervention: &intervention, ComputerID: turn.ComputerID, LocalAgentSessionID: turn.LocalAgentSessionID,
		}
		inserted, err := tx.NewInsert().Model(request).On("CONFLICT (id) DO NOTHING").Exec(ctx)
		if err != nil {
			return fmt.Errorf("create local agent permission request: %w", err)
		}
		if affected, err := inserted.RowsAffected(); err != nil || affected == 0 {
			return err
		}
		err = Submit(ctx, tx, a.enqueuer, locked.Scope.Conversation(), locked.Run, agentcontract.ToolCall{ID: requestID, Intervention: domain.ToolInterventionConfirmation})
		if errors.Is(err, agentcontract.ErrDecisionUnavailable) {
			if _, err := tx.NewUpdate().Model(request).
				Set("status = ?", domain.AgentToolCallCancelled).Set("completed_at = now()").
				WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("cancel local agent permission request: %w", err)
			}
			realtime.Notify(ctx, realtime.ComputerWork(workspaceID, computerID))
			return nil
		}
		return err
	})
}

// decideLocalAgentPermission 由处理人裁决本机 Agent 的权限请求：确认时选用允许一次的处理方式，拒绝时选用拒绝一次的处理方式，结果记为选用的处理方式编号并通知电脑领取；
// 裁决不唤醒 AI 员工。
func (a *Action) decideLocalAgentPermission(ctx context.Context, tx bun.Tx, locked lockedToolCall, subjectID string, approve bool) error {
	call := locked.Call
	var permission domain.LocalAgentPermission
	if err := json.Unmarshal([]byte(call.Arguments), &permission); err != nil {
		return fmt.Errorf("decode local agent permission: %w", err)
	}
	status, option := domain.AgentToolCallRejected, permission.Option(false)
	if approve {
		status, option = domain.AgentToolCallSucceeded, permission.Option(true)
	}
	if _, err := tx.NewUpdate().Model(call).
		Set("status = ?", status).
		Set("result = ?", option).
		Set("decided_by_subject_id = ?", subjectID).
		Set("decided_at = now()").
		Set("completed_at = now()").
		WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("decide local agent permission: %w", err)
	}
	return settleLocalAgentPermission(ctx, tx, locked, subjectID)
}

// settleLocalAgentPermission 在权限请求有了结果的事务中通知电脑领取裁决结果、处理人刷新待处理并登记会话时间线变化。
func settleLocalAgentPermission(ctx context.Context, tx bun.Tx, locked lockedToolCall, subjectIDs ...string) error {
	call := locked.Call
	if call.ComputerID != nil {
		realtime.Notify(ctx, realtime.ComputerWork(call.WorkspaceID, *call.ComputerID))
	}
	subjectIDs = slices.DeleteFunc(subjectIDs, func(id string) bool { return id == "" })
	if err := agentprocess.NotifyDecisionSubjects(ctx, tx, call.WorkspaceID, subjectIDs...); err != nil {
		return err
	}
	return chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline)
}

// finishLocalAgentTurn 在交给本机 Agent 的一轮有了结果的事务中，以 AI 员工身份把结果写入会话：成功时写入本机 Agent 的最终回复，失败时写入失败原因，
// 中断或取消时写入已中断提示；消息关联这一轮，同一轮只写入一次，不唤醒 AI 员工。同时记录本机 Agent 返回的 ACP 会话编号。调用方已锁定所属会话。
func (a *Action) finishLocalAgentTurn(ctx context.Context, tx bun.Tx, workspaceID, callID string) error {
	locked, err := a.lockToolCall(ctx, tx, workspaceID, callID)
	if err != nil {
		return err
	}
	call := locked.Call
	if call.LocalAgentSessionID == nil || call.Operation == nil {
		return nil
	}
	if outcome := call.Operation.Outcome; outcome != nil && outcome.AgentSession != "" {
		if _, err := tx.NewUpdate().Model((*servermodels.LocalAgentSession)(nil)).
			Set("session_id = ?", outcome.AgentSession).
			Where("workspace_id = ? AND id = ?", workspaceID, *call.LocalAgentSessionID).
			Exec(ctx); err != nil {
			return fmt.Errorf("record local agent session id: %w", err)
		}
	}
	messageType, body := domain.MessageTypeAgentCancelled, ""
	switch domain.AgentToolCallStatus(call.Status) {
	case domain.AgentToolCallSucceeded:
		messageType, body = domain.MessageTypeText, support.Deref(call.Result)
	case domain.AgentToolCallFailed:
		messageType, body = domain.MessageTypeAgentError, support.Deref(call.Error)
	}
	// 本机 Agent 没有给出回复正文时只在过程中展示结果。
	if messageType == domain.MessageTypeText && body == "" {
		return chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline)
	}
	participantID := locked.Scope.AgentParticipantID()
	var serviceSessionID *string
	if session := locked.Scope.ServiceSession(); session != nil {
		serviceSessionID = &session.ID
	}
	if participantID == "" {
		if participantID, err = agentmessage.EnsureCustomerParticipant(ctx, tx, workspaceID, locked.Run.ConversationID, locked.Run.AgentIdentityID); err != nil {
			return err
		}
	}
	key := "local-agent:" + call.ID
	_, _, err = agentmessage.Append(ctx, tx, a.enqueuer, locked.Scope.Conversation(), &servermodels.Message{
		ID: uuid.NewV7().String(), WorkspaceID: workspaceID, ConversationID: locked.Run.ConversationID, ServiceSessionID: serviceSessionID,
		SenderParticipantID: &participantID, Type: string(messageType), Body: body, IdempotencyKey: &key, AgentToolCallID: &call.ID,
	})
	return err
}
