//go:build server

package servicesession

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ReopenServiceSessionAction 重新打开已关闭的服务会话处理周期。
type ReopenServiceSessionAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewReopenServiceSessionAction 创建服务周期重新打开操作。
func NewReopenServiceSessionAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *ReopenServiceSessionAction {
	return &ReopenServiceSessionAction{db: db, enqueuer: enqueuer}
}

// Execute 重新打开当前处理周期并分配给当前身份。
func (a *ReopenServiceSessionAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (ServiceSessionResult, error) {
	conversationID, _ = str.NormalizeUUID(conversationID)
	var output ServiceSessionResult
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := chatstate.LockServiceSession(ctx, tx, identity.Workspace.ID, conversationID)
		if err != nil {
			return err
		}
		conversation, service, session := locked.Conversation, locked.Service, locked.Session
		if err := rejectServiceRequester(ctx, tx, service, identity.WorkspaceIdentity.ID); err != nil {
			return err
		}
		if domain.ServiceSessionStatus(session.Status) == domain.ServiceSessionStatusOpen {
			return &conversationaction.ConflictError{Reason: ConflictReasonServiceSessionAlreadyOpen}
		}
		// 写入时刻取持有会话锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		if err := servicestate.Begin(session).Reopen(now).Assign(identity.WorkspaceIdentity.ID, now).Save(ctx, tx, a.enqueuer); err != nil {
			return err
		}
		if err := appendServiceSessionEvent(ctx, tx, a.enqueuer, identity, conversation, session, domain.ServiceSource(service.Source), domain.ConversationSystemEventServiceSessionReopened, nil, nil); err != nil {
			return err
		}
		if err := servicesummary.MarkReopened(ctx, tx, session); err != nil {
			return err
		}
		if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
			return err
		}
		output = serviceSessionResult(session, &identity.WorkspaceIdentity)
		return nil
	})
	if err != nil {
		if pgerr.UniqueViolationOn(err, serverstorage.UniqueServiceSessionOpen) {
			err = &conversationaction.ConflictError{Reason: ConflictReasonServiceSessionAlreadyOpen}
		}
		return ServiceSessionResult{}, fmt.Errorf("reopen service session: %w", err)
	}
	return output, nil
}
