//go:build server

package directchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// FirstAgentTextMessageInput 定义 AI 草稿及首条消息。
type FirstAgentTextMessageInput struct {
	ConversationID  string
	AgentIdentityID string
	ClientMessageID string
	Body            string
}

// FirstAgentTextMessageResult 定义首次发送确认的会话和消息。
type FirstAgentTextMessageResult struct {
	Conversation inboxaction.ConversationSummary
	Message      conversationaction.ConversationMessage
}

// SendFirstAgentTextMessageAction 在首次发送时原子创建 AI 聊天。
type SendFirstAgentTextMessageAction struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	scheduler conversationaction.AgentChatMessageScheduler
}

// NewSendFirstAgentTextMessageAction 创建 AI 聊天首次发送操作。
func NewSendFirstAgentTextMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer, scheduler conversationaction.AgentChatMessageScheduler) *SendFirstAgentTextMessageAction {
	return &SendFirstAgentTextMessageAction{db: db, enqueuer: enqueuer, scheduler: scheduler}
}

// Execute 按草稿稳定编号创建会话并幂等保存消息。
func (a *SendFirstAgentTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input FirstAgentTextMessageInput) (FirstAgentTextMessageResult, error) {
	messageInput := normalizeInternalMessageInput(InternalTextMessageInput{ConversationID: input.ConversationID, ClientMessageID: input.ClientMessageID, Body: input.Body})
	agentID, _ := str.NormalizeUUID(input.AgentIdentityID)
	var result FirstAgentTextMessageResult
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 发送资格校验前先按发送编号读取本人保存的消息。
		saved, found, err := replayInternalMessage(ctx, tx, identity, messageInput.message())
		if err != nil {
			return err
		}
		if found {
			if err := lockDraftOwnership(ctx, tx, identity, messageInput.ConversationID, agentID, ""); err != nil {
				return err
			}
			result.Message = saved
			result.Conversation, err = inboxaction.NewLoadInboxQuery(tx).LoadAgentConversation(ctx, identity, messageInput.ConversationID)
			return err
		}
		if err := ensureAgentConversation(ctx, tx, identity, messageInput.ConversationID, agentID, messageInput.Body); err != nil {
			return err
		}
		sendContext, err := lockAgentSendContext(ctx, tx, identity, messageInput.ConversationID)
		if err != nil {
			return err
		}
		result.Message, err = saveInternalMessage(ctx, tx, a.enqueuer, identity, messageInput.message(), sendContext, a.scheduler)
		if err != nil {
			return err
		}
		result.Conversation, err = inboxaction.NewLoadInboxQuery(tx).LoadAgentConversation(ctx, identity, messageInput.ConversationID)
		return err
	})
	if err != nil {
		return FirstAgentTextMessageResult{}, err
	}
	return result, nil
}

// ensureAgentConversation 创建与 AI 员工的聊天，重试时核对固定的业务归属。
func ensureAgentConversation(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, conversationID, agentID, body string) error {
	// 锁定已有草稿并核对归属后复用共享主体。
	if found, err := lockAgentConversationDraft(ctx, tx, identity, conversationID, agentID); err != nil || found {
		return err
	}
	// 个人 AI 员工只能由负责人发起聊天。
	var target servermodels.WorkspaceIdentity
	err := tx.NewSelect().Model(&target).
		Join("JOIN agents AS agent ON agent.workspace_id = oi.workspace_id AND agent.identity_id = oi.id").
		Where("oi.workspace_id = ? AND oi.id = ?", identity.Workspace.ID, agentID).
		Where("oi.type = ? AND (NOT ? = ANY(agent.service_audiences) OR agent.responsible_user_id = ?)", domain.WorkspaceIdentityTypeAgent, domain.ServiceAudiencePersonal, identity.User.ID).
		Where("agent.status = ?", domain.IdentityStatusActive).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return conversationaction.ErrAgentTargetNotFound
	}
	if err != nil {
		return fmt.Errorf("load AI chat target: %w", err)
	}
	subjects, err := chatstate.EnsureSubjects(ctx, tx, identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, []string{identity.WorkspaceIdentity.ID, agentID})
	if err != nil {
		return err
	}
	userSubject, agentSubject := subjects[identity.WorkspaceIdentity.ID], subjects[agentID]
	titleText := conversationTitle(body)
	cv := &servermodels.Conversation{ID: conversationID, WorkspaceID: identity.Workspace.ID, Type: string(domain.ConversationTypeAgent), Status: string(domain.ConversationStatusActive), Title: &titleText, CreatedBySubjectID: &userSubject.ID}
	inserted, err := tx.NewInsert().Model(cv).Column("id", "workspace_id", "type", "status", "title", "created_by_subject_id").On("CONFLICT (id) DO NOTHING").Exec(ctx)
	if err != nil {
		return fmt.Errorf("create AI conversation: %w", err)
	}
	count, err := inserted.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		found, err := lockAgentConversationDraft(ctx, tx, identity, conversationID, agentID)
		if err != nil {
			return err
		}
		if !found {
			return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
		}
		return nil
	}
	relation := &servermodels.AgentConversation{ConversationID: conversationID, WorkspaceID: identity.Workspace.ID, UserIdentityID: identity.WorkspaceIdentity.ID, AgentIdentityID: agentID}
	if _, err := tx.NewInsert().Model(relation).Column("conversation_id", "workspace_id", "user_identity_id", "agent_identity_id").Exec(ctx); err != nil {
		return fmt.Errorf("create AI conversation relation: %w", err)
	}
	participants := []*servermodels.ConversationParticipant{
		{ID: uuid.NewV7().String(), WorkspaceID: identity.Workspace.ID, ConversationID: conversationID, SubjectID: userSubject.ID, Role: string(domain.ConversationParticipantRoleMember)},
		{ID: uuid.NewV7().String(), WorkspaceID: identity.Workspace.ID, ConversationID: conversationID, SubjectID: agentSubject.ID, Role: string(domain.ConversationParticipantRoleMember)},
	}
	if _, err := tx.NewInsert().Model(&participants).Column("id", "workspace_id", "conversation_id", "subject_id", "role").Exec(ctx); err != nil {
		return fmt.Errorf("create AI conversation participants: %w", err)
	}
	return nil
}

// lockAgentConversationDraft 锁定已有草稿编号并核对首次发送的业务归属。
func lockAgentConversationDraft(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, conversationID, agentID string) (bool, error) {
	cv, err := chatstate.LockConversation(ctx, tx, identity.Workspace.ID, conversationID)
	if errors.Is(err, chatstate.ErrConversationNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	matches, err := tx.NewSelect().Model((*servermodels.AgentConversation)(nil)).
		Where("ac.conversation_id = ? AND ac.workspace_id = ? AND ac.user_identity_id = ? AND ac.agent_identity_id = ?", conversationID, identity.Workspace.ID, identity.WorkspaceIdentity.ID, agentID).Exists(ctx)
	if err != nil {
		return false, err
	}
	if !matches || cv.Type != string(domain.ConversationTypeAgent) || cv.Status != string(domain.ConversationStatusActive) {
		return false, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	return true, nil
}

// SendAgentTextMessageAction 向已有 AI 聊天发送成员消息。
type SendAgentTextMessageAction struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	scheduler conversationaction.AgentChatMessageScheduler
}

// lockDraftOwnership 锁定按草稿编号首发的 AI 聊天或 Copilot 线程并核对其固定归属，servedConversationID 非空时核对 Copilot 线程；不核对 AI 员工当前是否可用。
func lockDraftOwnership(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, conversationID, agentID, servedConversationID string) error {
	var found bool
	var err error
	if servedConversationID != "" {
		found, err = lockServiceCopilotThreadDraft(ctx, tx, identity, conversationID, servedConversationID, agentID)
	} else {
		found, err = lockAgentConversationDraft(ctx, tx, identity, conversationID, agentID)
	}
	if err != nil {
		return err
	}
	if !found {
		return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	return nil
}

// NewSendAgentTextMessageAction 创建已有 AI 聊天发送操作。
func NewSendAgentTextMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer, scheduler conversationaction.AgentChatMessageScheduler) *SendAgentTextMessageAction {
	return &SendAgentTextMessageAction{db: db, enqueuer: enqueuer, scheduler: scheduler}
}

// Execute 校验 AI 会话归属并在事务中保存成员消息及执行输入。
func (a *SendAgentTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input InternalTextMessageInput) (conversationaction.ConversationMessage, error) {
	normalized := normalizeInternalMessageInput(input)
	var result conversationaction.ConversationMessage
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 发送资格校验前先按发送编号读取本人保存的消息。
		saved, found, err := replayInternalMessage(ctx, tx, identity, normalized.message())
		if err != nil || found {
			result = saved
			return err
		}
		sendContext, err := lockAgentSendContext(ctx, tx, identity, normalized.ConversationID)
		if err != nil {
			return err
		}
		result, err = saveInternalMessage(ctx, tx, a.enqueuer, identity, normalized.message(), sendContext, a.scheduler)
		return err
	})
	return result, err
}

// lockAgentSendContext 锁定会话与成员后复核 AI 会话归属和发送资格：AI 员工有效，或会话有进行中的服务周期。
func lockAgentSendContext(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, conversationID string) (internalMessageContext, error) {
	row := internalMessageContext{}
	member, err := chatstate.LockMember(ctx, tx, identity, conversationID)
	if err != nil {
		return row, err
	}
	err = tx.NewSelect().TableExpr("agent_conversations AS ac").
		ColumnExpr("ac.conversation_id, mine.id AS participant_id, mine.subject_id, ac.agent_identity_id, agent.active_revision_id AS agent_revision_id, agent.paused_at IS NOT NULL AS agent_paused, agent_computer.revoked_at IS NOT NULL AS agent_unbound").
		ColumnExpr("agent.status = ? AS agent_active", domain.IdentityStatusActive).
		ColumnExpr(`EXISTS (
			SELECT 1 FROM service_conversations AS svc
			JOIN service_sessions AS current ON current.workspace_id = svc.workspace_id AND current.id = svc.current_service_session_id
			WHERE svc.workspace_id = ac.workspace_id AND svc.conversation_id = ac.conversation_id AND current.status = ?
		) AS service_open`, domain.ServiceSessionStatusOpen).
		Join("JOIN conversations AS cv ON cv.id = ac.conversation_id AND cv.workspace_id = ac.workspace_id").
		Join("JOIN chat_subjects AS user_cs ON user_cs.workspace_id = ac.workspace_id AND user_cs.kind = ? AND user_cs.source_id = ac.user_identity_id", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN conversation_participants AS mine ON mine.workspace_id = ac.workspace_id AND mine.conversation_id = ac.conversation_id AND mine.subject_id = user_cs.id AND mine.left_at IS NULL").
		Join("JOIN chat_subjects AS agent_cs ON agent_cs.workspace_id = ac.workspace_id AND agent_cs.kind = ? AND agent_cs.source_id = ac.agent_identity_id", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN conversation_participants AS peer ON peer.workspace_id = ac.workspace_id AND peer.conversation_id = ac.conversation_id AND peer.subject_id = agent_cs.id AND peer.left_at IS NULL").
		Join("JOIN agents AS agent ON agent.workspace_id = ac.workspace_id AND agent.identity_id = ac.agent_identity_id").
		Join("LEFT JOIN computers AS agent_computer ON agent_computer.workspace_id = agent.workspace_id AND agent_computer.id = agent.computer_id").
		Where("ac.workspace_id = ? AND ac.conversation_id = ? AND ac.user_identity_id = ?", identity.Workspace.ID, conversationID, identity.WorkspaceIdentity.ID).
		Where("cv.type = ? AND cv.status = ?", domain.ConversationTypeAgent, domain.ConversationStatusActive).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return row, conversationaction.ErrConversationNotFound
	}
	if err != nil {
		return row, fmt.Errorf("load AI conversation send context: %w", err)
	}
	// AI 员工停用后只能继续进行中的服务周期，由负责的真人接着处理。
	if !row.AgentActive && !row.ServiceOpen {
		return row, conversationaction.ErrConversationNotFound
	}
	// 电脑已撤销优先于暂停。
	if row.AgentUnbound {
		return row, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonPersonalAgentUnbound}
	}
	if row.AgentPaused {
		return row, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonPersonalAgentPaused}
	}
	row.Conversation = member.Conversation
	row.AgentInputKind = domain.AgentInputKindAgentDirect
	return row, nil
}

// conversationTitle 取首条消息正文合并空白后的前 40 个字符作为 AI 聊天与 Copilot 线程标题。
func conversationTitle(body string) string {
	return str.Substr(str.Squish(body), 0, 40)
}
