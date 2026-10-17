//go:build server

package servicesession

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ClaimServiceSessionAction 领取或接管服务会话当前处理周期。
type ClaimServiceSessionAction struct {
	db        *bun.DB
	canceller ServiceSessionRunCanceller
	enqueuer  servertask.TxEnqueuer
}

// NewClaimServiceSessionAction 创建服务周期领取操作。
func NewClaimServiceSessionAction(db *bun.DB, canceller ServiceSessionRunCanceller, enqueuer servertask.TxEnqueuer) *ClaimServiceSessionAction {
	return &ClaimServiceSessionAction{db: db, canceller: canceller, enqueuer: enqueuer}
}

// Execute 把未关闭处理周期负责人设置为当前身份，接管时为原负责人补分配。
func (a *ClaimServiceSessionAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (ServiceSessionResult, error) {
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
		if err := rejectServiceRequester(ctx, tx, service, identity.WorkspaceIdentity.ID); err != nil {
			return err
		}
		// 写入时刻取持有会话锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		if session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != identity.WorkspaceIdentity.ID {
			previousAssigneeID := session.AssigneeIdentityID
			if session.AssigneeIdentityID != nil {
				cancelledRunIDs, err = a.canceller.CancelForServiceSession(
					ctx, tx, session.WorkspaceID, session.ID,
					*session.AssigneeIdentityID, domain.AgentRunErrorCodeAssigneeChanged,
				)
				if err != nil {
					return err
				}
				cancelledSession = session
			}
			if err := servicestate.Begin(session).Assign(identity.WorkspaceIdentity.ID, now).Save(ctx, tx, a.enqueuer); err != nil {
				return err
			}
			// 无人负责时记为领取，已有负责人时记为接管。
			eventType := domain.ConversationSystemEventServiceSessionClaimed
			if previousAssigneeID != nil {
				eventType = domain.ConversationSystemEventServiceSessionTakenOver
			}
			if err := appendServiceSessionEvent(ctx, tx, a.enqueuer, identity, conversation, session, domain.ServiceSource(service.Source), eventType, previousAssigneeID, nil); err != nil {
				return err
			}
			if previousAssigneeID != nil {
				if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, serviceassignment.BackfillInput{WorkspaceID: session.WorkspaceID, IdentityID: *previousAssigneeID}); err != nil {
					return err
				}
			}
			if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
				return err
			}
		}
		output = serviceSessionResult(session, &identity.WorkspaceIdentity)
		return nil
	})
	if err != nil {
		return ServiceSessionResult{}, fmt.Errorf("claim service session: %w", err)
	}
	logServiceSessionAgentCancellation(ctx, cancelledRunIDs, cancelledSession, domain.AgentRunErrorCodeAssigneeChanged)
	return output, nil
}
