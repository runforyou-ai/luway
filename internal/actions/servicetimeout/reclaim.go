//go:build server

// Package servicetimeout 按企业超时设置提醒负责人和队列客服处理等待中的客户，回收负责人超时未回复的客服处理周期，并在客户超时未回复 AI 员工时跟进与关单。
package servicetimeout

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

func (w *Worker) reclaim(ctx context.Context, snapshot *servermodels.ServiceSession) error {
	previousAssigneeID := *snapshot.AssigneeIdentityID
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		member, err := serviceassignment.LockQueueMember(ctx, tx, snapshot.WorkspaceID, snapshot.ID, snapshot.TeamID, previousAssigneeID)
		if err != nil {
			return err
		}
		loaded, err := loadSession(ctx, tx, snapshot.WorkspaceID, snapshot.ID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		conversation, session := loaded.Locked.Conversation, loaded.Session
		// 负责人或队列在读取后已变化时由引起变化的操作负责后续处理。
		if loaded.dueAction() != actionReclaim ||
			*session.AssigneeIdentityID != previousAssigneeID || !chatstate.SameTeam(session.TeamID, snapshot.TeamID) {
			return nil
		}
		previous := &servermodels.WorkspaceIdentity{}
		if err := tx.NewSelect().Model(previous).Column("oi.id", "oi.display_name").
			Where("oi.workspace_id = ? AND oi.id = ?", session.WorkspaceID, previousAssigneeID).
			Scan(ctx); err != nil {
			return fmt.Errorf("load reclaimed assignee: %w", err)
		}
		target, err := serviceroute.ServiceSessionQueueTarget(ctx, tx, session)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(domain.ServiceSessionReturnedEvent{
			ServiceSessionID: session.ID, FromIdentityID: previous.ID, FromDisplayName: previous.DisplayName,
			Target: target, Reason: domain.ServiceSessionReturnResponseTimeout,
		})
		if err != nil {
			return fmt.Errorf("encode service session returned event: %w", err)
		}
		eventType := string(domain.ConversationSystemEventServiceSessionReturned)
		if _, _, err := chatstate.AppendSystemEvent(ctx, tx, conversation, &servermodels.Message{
			ID: uuid.NewV7().String(), WorkspaceID: session.WorkspaceID, ConversationID: session.ConversationID,
			ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
			SystemEventType: &eventType, SystemEventPayload: payload,
		}); err != nil {
			return fmt.Errorf("append service session returned event: %w", err)
		}
		if _, err := chatstate.AppendRequesterStatus(ctx, tx, w.enqueuer, conversation, session, loaded.Locked.Source(), domain.ServiceRequestStatusHandedOff, &target, nil); err != nil {
			return err
		}
		if err := servicestate.Begin(session).Queue(session.TeamID, loaded.Now).Save(ctx, tx, w.enqueuer); err != nil {
			return err
		}
		// 没有候选时投递排除原负责人的分配任务，覆盖回收提交前已完成补分配的成员。
		if member != nil {
			if err := serviceassignment.Assign(ctx, tx, w.enqueuer, loaded.Locked, member); err != nil {
				return err
			}
		} else if err := serviceassignment.EnqueueAssign(ctx, tx, w.enqueuer, serviceassignment.AssignInput{
			WorkspaceID: session.WorkspaceID, ServiceSessionID: session.ID, ExcludeIdentityID: previousAssigneeID,
		}); err != nil {
			return err
		}
		if err := serviceassignment.EnqueueBackfill(ctx, tx, w.enqueuer, serviceassignment.BackfillInput{
			WorkspaceID: session.WorkspaceID, IdentityID: previousAssigneeID, ExcludeServiceSessionID: session.ID,
		}); err != nil {
			return err
		}
		// 读取原负责人账号，提醒其周期已退回队列。
		var previousUserID string
		if err := tx.NewSelect().Model((*servermodels.User)(nil)).Column("u.id").
			Where("u.workspace_id = ? AND u.identity_id = ?", session.WorkspaceID, previousAssigneeID).
			Scan(ctx, &previousUserID); err != nil {
			return fmt.Errorf("load reclaimed assignee user: %w", err)
		}
		if err := notificationtask.EnqueueServiceAttention(ctx, tx, w.enqueuer, session, previousUserID, domain.ServiceAttentionReturned); err != nil {
			return err
		}
		slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "负责人超时未回复，客服处理周期已退回队列",
			"conversation_id", session.ConversationID,
			"service_session_id", session.ID, "previous_assignee_identity_id", previousAssigneeID,
			"target_kind", target.Kind, "reassigned", member != nil)
		return nil
	})
}

// handleAgentIdle 在事务中锁定周期并复核客户仍未回复 AI 负责人：到达跟进时长时追加一次超时跟进输入，确认请求后到达关单时长时按客户失联关闭周期。
func (w *Worker) handleAgentIdle(ctx context.Context, input ProcessInput) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		loaded, err := loadSession(ctx, tx, input.WorkspaceID, input.ServiceSessionID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		session := loaded.Session
		switch loaded.dueAction() {
		case actionFollowUp:
			scheduled, err := w.scheduler.ScheduleCustomerFollowUp(ctx, tx, session)
			if err != nil {
				return err
			}
			slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "客户超时未回复 AI 员工，处理超时跟进",
				"conversation_id", session.ConversationID,
				"service_session_id", session.ID, "assignee_identity_id", *session.AssigneeIdentityID, "scheduled", scheduled)
		case actionCloseUnresponsive:
			if err := servicesessionaction.CloseAgentServiceSession(ctx, tx, w.enqueuer, loaded.Locked.Conversation, session, loaded.Locked.Source(), domain.ServiceSessionCloseCustomerUnresponsive); err != nil {
				return err
			}
			slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "客户确认请求后超时未回复，客服处理周期已关闭",
				"conversation_id", session.ConversationID,
				"service_session_id", session.ID, "assignee_identity_id", *session.AssigneeIdentityID)
		}
		return nil
	})
}
