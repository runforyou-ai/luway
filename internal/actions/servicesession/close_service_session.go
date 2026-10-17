//go:build server

package servicesession

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// CloseServiceSessionAction 关闭服务会话当前处理周期。
type CloseServiceSessionAction struct {
	db        *bun.DB
	canceller ServiceSessionRunCanceller
	enqueuer  servertask.TxEnqueuer
}

// NewCloseServiceSessionAction 创建服务周期关闭操作。
func NewCloseServiceSessionAction(db *bun.DB, canceller ServiceSessionRunCanceller, enqueuer servertask.TxEnqueuer) *CloseServiceSessionAction {
	return &CloseServiceSessionAction{db: db, canceller: canceller, enqueuer: enqueuer}
}

// Execute 关闭公共队列或当前身份负责的处理周期，无人负责的周期由关闭人成为负责人；关闭本人负责的周期后为本人补分配。
func (a *CloseServiceSessionAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (ServiceSessionResult, error) {
	conversationID, _ = str.NormalizeUUID(conversationID)
	var output ServiceSessionResult
	var cancelledRunIDs []string
	var cancelledSession *servermodels.ServiceSession
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := lockOpenServiceSession(ctx, tx, identity.Workspace.ID, conversationID)
		if err != nil {
			return err
		}
		conversation, service, session := locked.Conversation, locked.Service, locked.Session
		if session.AssigneeIdentityID != nil && *session.AssigneeIdentityID != identity.WorkspaceIdentity.ID {
			return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionOwned}
		}
		if err := rejectServiceRequester(ctx, tx, service, identity.WorkspaceIdentity.ID); err != nil {
			return err
		}
		if session.AssigneeIdentityID != nil {
			cancelledRunIDs, err = a.canceller.CancelForServiceSession(
				ctx, tx, session.WorkspaceID, session.ID,
				*session.AssigneeIdentityID, domain.AgentRunErrorCodeSessionClosed,
			)
			if err != nil {
				return err
			}
			cancelledSession = session
		}
		// 写入时刻取持有会话锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		assigneeIdentityID := identity.WorkspaceIdentity.ID
		ownedBefore := session.AssigneeIdentityID != nil
		// 无人负责的周期由关闭人领取后关闭。
		transition := servicestate.Begin(session)
		if !ownedBefore {
			transition.Assign(assigneeIdentityID, now)
		}
		if err := transition.Close(assigneeIdentityID, domain.ServiceSessionCloseManual, now).Save(ctx, tx, a.enqueuer); err != nil {
			return err
		}
		if err := appendServiceSessionClosedEvent(ctx, tx, a.enqueuer, conversation, session, domain.ServiceSource(service.Source), identity.WorkspaceIdentity.ID, identity.WorkspaceIdentity.DisplayName, domain.ServiceSessionCloseManual); err != nil {
			return err
		}
		if err := servicesummary.MarkClosed(ctx, tx, a.enqueuer, session, domain.ServiceSessionCloseManual); err != nil {
			return err
		}
		if ownedBefore {
			if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, serviceassignment.BackfillInput{WorkspaceID: session.WorkspaceID, IdentityID: assigneeIdentityID}); err != nil {
				return err
			}
		}
		if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
			return err
		}
		// 读取处理周期负责人身份。
		assignee := &servermodels.WorkspaceIdentity{}
		if err := tx.NewSelect().Model(assignee).
			Column("oi.id", "oi.type", "oi.display_name", "oi.avatar_file_id").
			Where("oi.workspace_id = ?", identity.Workspace.ID).
			Where("oi.id = ?", assigneeIdentityID).
			Scan(ctx); err != nil {
			return err
		}
		output = serviceSessionResult(session, assignee)
		return nil
	})
	if err != nil {
		return ServiceSessionResult{}, fmt.Errorf("close service session: %w", err)
	}
	logServiceSessionAgentCancellation(ctx, cancelledRunIDs, cancelledSession, domain.AgentRunErrorCodeSessionClosed)
	return output, nil
}

// CloseAgentServiceSession 在调用方持有会话锁的事务中关闭 AI 员工负责的开放周期：写入结束方式与关闭事件并准备小结，关闭人为负责的 AI 员工。
func CloseAgentServiceSession(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, session *servermodels.ServiceSession, source domain.ServiceSource, reason domain.ServiceSessionCloseReason) error {
	if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen || session.AssigneeIdentityID == nil {
		return conversationaction.ErrDataInvariant
	}
	agent := &servermodels.WorkspaceIdentity{}
	if err := db.NewSelect().Model(agent).Column("oi.id", "oi.display_name").
		Where("oi.workspace_id = ? AND oi.id = ? AND oi.type = ?", session.WorkspaceID, *session.AssigneeIdentityID, domain.WorkspaceIdentityTypeAgent).
		Scan(ctx); err != nil {
		return fmt.Errorf("load closing agent identity: %w", err)
	}
	// 写入时刻取持有会话锁之后的数据库时刻。
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return err
	}
	if err := servicestate.Begin(session).Close(agent.ID, reason, now).Save(ctx, db, enqueuer); err != nil {
		return err
	}
	if err := appendServiceSessionClosedEvent(ctx, db, enqueuer, conversation, session, source, agent.ID, agent.DisplayName, reason); err != nil {
		return err
	}
	if err := servicesummary.MarkClosed(ctx, db, enqueuer, session, reason); err != nil {
		return err
	}
	return chatstate.TouchConversation(ctx, db, conversation, domain.ConversationChangeService)
}
