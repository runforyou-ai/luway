//go:build server

package tooldecision

import (
	"context"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Review 由 AI 员工的负责人把待核对的工具调用标记为已核对。
func (a *Action) Review(ctx context.Context, identity *servermodels.Identity, callID string) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := a.lockToolCall(ctx, tx, identity.Workspace.ID, callID)
		if err != nil {
			return err
		}
		approver, err := LoadApprover(ctx, tx, identity.Workspace.ID, locked.Run.AgentIdentityID)
		if err != nil {
			return err
		}
		if approver.UserID != identity.User.ID {
			return ErrToolCallUnavailable
		}
		if locked.Call.Status != string(domain.AgentToolCallNeedsReview) {
			return ErrToolCallDecided
		}
		subject, err := chatstate.EnsureSubject(ctx, tx, identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID, uuid.NewV7().String())
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(locked.Call).
			Set("status = ?", domain.AgentToolCallReviewed).
			Set("decided_by_subject_id = ?", subject.ID).
			Set("decided_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("review agent tool call: %w", err)
		}
		realtime.Notify(ctx, realtime.UserToolDecisionsChanged(identity.Workspace.ID, identity.User.ID))
		return chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline)
	})
}
