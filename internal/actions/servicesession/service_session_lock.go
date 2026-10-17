//go:build server

package servicesession

import (
	"context"
	"errors"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// lockActiveServiceHandler 锁定当前身份的有效账号并校验其已开启处理服务请求。
func lockActiveServiceHandler(ctx context.Context, tx bun.Tx, identity *servermodels.Identity) error {
	err := identityaction.LockActiveServiceHandlingUser(ctx, tx, identity)
	if errors.Is(err, identityaction.ErrServiceHandlingRequired) {
		return &conversationaction.ConflictError{Reason: ConflictReasonServiceHandlingRequired}
	}
	return err
}

// lockOpenServiceSession 锁定服务会话及其最新且未关闭的服务周期。
func lockOpenServiceSession(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (chatstate.LockedServiceSession, error) {
	locked, err := chatstate.LockServiceSession(ctx, db, workspaceID, conversationID)
	if err != nil {
		return chatstate.LockedServiceSession{}, err
	}
	if domain.ServiceSessionStatus(locked.Session.Status) != domain.ServiceSessionStatusOpen {
		return chatstate.LockedServiceSession{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionNotReplyable}
	}
	return locked, nil
}

// serviceSessionResult 转换服务周期命令结果。
func serviceSessionResult(session *servermodels.ServiceSession, assignee *servermodels.WorkspaceIdentity) ServiceSessionResult {
	resultAssignee := support.MapPtr(assignee, func(assignee servermodels.WorkspaceIdentity) ServiceSessionAssignee {
		return ServiceSessionAssignee{IdentityID: assignee.ID, Type: domain.WorkspaceIdentityType(assignee.Type), DisplayName: assignee.DisplayName, AvatarFileID: assignee.AvatarFileID}
	})
	return ServiceSessionResult{ID: session.ID, Status: domain.ServiceSessionStatus(session.Status), Assignee: resultAssignee, ClosedAt: session.ClosedAt}
}

// logServiceSessionAgentCancellation 在事务提交后记录被取消的客服会话 Agent 运行。
func logServiceSessionAgentCancellation(ctx context.Context, runIDs []string, session *servermodels.ServiceSession, reason domain.AgentRunErrorCode) {
	for _, runID := range runIDs {
		slog.InfoContext(ctx, "已取消客服会话 Agent 运行",
			"agent_run_id", runID,
			"service_session_id", session.ID,
			"conversation_id", session.ConversationID,
			"reason", reason,
		)
	}
}
