//go:build server

package agentrun

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// ScheduleGroupMentions 在群消息事务内按点名顺序为被点名的 Agent 追加输入。
func (s *Scheduler) ScheduleGroupMentions(ctx context.Context, db bun.IDB, workspaceID, conversationID, messageID, senderSubjectID string, agentIdentityIDs []string) error {
	for ordinal, agentIdentityID := range agentIdentityIDs {
		revisionID, eligible, err := loadGroupAgentRevision(ctx, db, workspaceID, conversationID, agentIdentityID, true)
		if err != nil {
			return err
		}
		if !eligible {
			slog.WarnContext(logscope.WithWorkspace(ctx, workspaceID), "群内被点名的 AI 员工不满足执行资格",
				"conversation_id", conversationID,
				"agent_identity_id", agentIdentityID,
				"message_id", messageID,
			)
			continue
		}
		if err := s.appendInput(ctx, db, agentRunSpec{
			WorkspaceID: workspaceID, ConversationID: conversationID,
			AgentIdentityID: agentIdentityID, RevisionID: revisionID,
			ScopeKind: domain.AgentExecutionScopeConversation, ScopeID: conversationID,
			Kind: domain.AgentInputKindMention, SourceSubjectID: senderSubjectID, SourceOrdinal: ordinal,
		}, messageID); err != nil {
			return fmt.Errorf("schedule group agent mention: %w", err)
		}
	}
	return nil
}
