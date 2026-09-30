//go:build server

package directchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ServiceCopilotThread 定义服务会话中 Copilot 线程的摘要。
type ServiceCopilotThread struct {
	ID                  string                `bun:"id"`
	Title               string                `bun:"title"`
	AgentIdentityID     string                `bun:"agent_identity_id"`
	AgentName           string                `bun:"agent_name"`
	AgentAvatarFileID   *string               `bun:"agent_avatar_file_id"`
	AgentStatus         domain.IdentityStatus `bun:"agent_status"`
	CreatedByIdentityID string                `bun:"created_by_identity_id"`
	CreatedByName       string                `bun:"created_by_name"`
	CreatedAt           time.Time             `bun:"created_at"`
	LastActivityAt      time.Time             `bun:"last_activity_at"`
}

// FirstServiceCopilotMessageInput 定义新线程的稳定编号、所属服务会话、回答的 AI 员工和首条提问。
type FirstServiceCopilotMessageInput struct {
	ThreadID             string
	ServedConversationID string
	AgentIdentityID      string
	ClientMessageID      string
	Body                 string
}

// FirstServiceCopilotMessageResult 定义首条提问确认的线程和消息。
type FirstServiceCopilotMessageResult struct {
	Thread  ServiceCopilotThread
	Message conversationaction.ConversationMessage
}

// SendFirstServiceCopilotMessageAction 以首条提问原子创建 Copilot 线程。
type SendFirstServiceCopilotMessageAction struct {
	db        *bun.DB
	scheduler conversationaction.AgentChatMessageScheduler
}

// NewSendFirstServiceCopilotMessageAction 创建 Copilot 线程首条提问操作。
func NewSendFirstServiceCopilotMessageAction(db *bun.DB, scheduler conversationaction.AgentChatMessageScheduler) *SendFirstServiceCopilotMessageAction {
	return &SendFirstServiceCopilotMessageAction{db: db, scheduler: scheduler}
}

// Execute 按线程稳定编号创建线程并幂等保存首条提问。
func (a *SendFirstServiceCopilotMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input FirstServiceCopilotMessageInput) (FirstServiceCopilotMessageResult, error) {
	messageInput, fields := normalizeInternalMessageInput(InternalTextMessageInput{ConversationID: input.ThreadID, ClientMessageID: input.ClientMessageID, Body: input.Body})
	servedConversationID, valid := common.NormalizeUUID(input.ServedConversationID)
	if !valid {
		fields["servedConversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	agentID, valid := common.NormalizeUUID(input.AgentIdentityID)
	if !valid {
		fields["agentIdentityId"] = conversationaction.ValidationTargetIdentityIDInvalid
	}
	if len(fields) > 0 {
		return FirstServiceCopilotMessageResult{}, &conversationaction.ValidationError{Fields: fields}
	}
	var result FirstServiceCopilotMessageResult
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := ensureServiceCopilotThread(ctx, tx, identity, messageInput.ConversationID, servedConversationID, agentID, messageInput.Body); err != nil {
			return err
		}
		sendContext, err := lockServiceCopilotSendContext(ctx, tx, identity, messageInput.ConversationID)
		if err != nil {
			return err
		}
		result.Message, err = saveInternalTextMessage(ctx, tx, identity, messageInput, sendContext, a.scheduler)
		if err != nil {
			return err
		}
		threads, err := loadServiceCopilotThreads(ctx, tx, identity.Organization.ID, servedConversationID, messageInput.ConversationID)
		if err != nil {
			return err
		}
		if len(threads) != 1 {
			return conversationaction.ErrDataInvariant
		}
		result.Thread = threads[0]
		return nil
	})
	if err != nil {
		return FirstServiceCopilotMessageResult{}, err
	}
	return result, nil
}

// SendServiceCopilotTextMessageAction 向已有 Copilot 线程发送成员提问。
type SendServiceCopilotTextMessageAction struct {
	db        *bun.DB
	scheduler conversationaction.AgentChatMessageScheduler
}

// NewSendServiceCopilotTextMessageAction 创建 Copilot 线程提问操作。
func NewSendServiceCopilotTextMessageAction(db *bun.DB, scheduler conversationaction.AgentChatMessageScheduler) *SendServiceCopilotTextMessageAction {
	return &SendServiceCopilotTextMessageAction{db: db, scheduler: scheduler}
}

// Execute 校验线程归属与 AI 员工可用后，在事务中保存提问及执行输入。
func (a *SendServiceCopilotTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input InternalTextMessageInput) (conversationaction.ConversationMessage, error) {
	normalized, fields := normalizeInternalMessageInput(input)
	if len(fields) > 0 {
		return conversationaction.ConversationMessage{}, &conversationaction.ValidationError{Fields: fields}
	}
	var result conversationaction.ConversationMessage
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		sendContext, err := lockServiceCopilotSendContext(ctx, tx, identity, normalized.ConversationID)
		if err != nil {
			return err
		}
		result, err = saveInternalTextMessage(ctx, tx, identity, normalized, sendContext, a.scheduler)
		return err
	})
	return result, err
}

// ListServiceCopilotThreadsQuery 读取服务会话中的 Copilot 线程。
type ListServiceCopilotThreadsQuery struct {
	db *bun.DB
}

// NewListServiceCopilotThreadsQuery 创建 Copilot 线程列表查询。
func NewListServiceCopilotThreadsQuery(db *bun.DB) *ListServiceCopilotThreadsQuery {
	return &ListServiceCopilotThreadsQuery{db: db}
}

// Execute 校验客户会话属于当前企业后，按最近活动倒序返回全部线程。
func (q *ListServiceCopilotThreadsQuery) Execute(ctx context.Context, identity *servermodels.Identity, servedConversationID string) ([]ServiceCopilotThread, error) {
	if !common.ValidUUID(servedConversationID) {
		return nil, conversationaction.ErrConversationNotFound
	}
	var threads []ServiceCopilotThread
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := conversationaction.AuthorizeConversationHistory(ctx, tx, identity, servedConversationID); err != nil {
			return err
		}
		var err error
		threads, err = loadServiceCopilotThreads(ctx, tx, identity.Organization.ID, servedConversationID, "")
		return err
	})
	if err != nil {
		return nil, err
	}
	return threads, nil
}

// loadServiceCopilotThreads 读取服务会话的线程摘要，threadID 非空时只读取该线程。
func loadServiceCopilotThreads(ctx context.Context, db bun.IDB, organizationID, servedConversationID, threadID string) ([]ServiceCopilotThread, error) {
	threads := make([]ServiceCopilotThread, 0)
	query := db.NewSelect().
		TableExpr("service_copilot_threads AS sct").
		ColumnExpr("cv.id::text AS id, COALESCE(cv.title, '') AS title").
		ColumnExpr("sct.agent_identity_id::text AS agent_identity_id, agent_oi.display_name AS agent_name, agent_oi.avatar_file_id::text AS agent_avatar_file_id, agent.status AS agent_status").
		ColumnExpr("sct.created_by_identity_id::text AS created_by_identity_id, creator.display_name AS created_by_name").
		ColumnExpr("sct.created_at, COALESCE(cv.last_activity_at, sct.created_at) AS last_activity_at").
		Join("JOIN conversations AS cv ON cv.organization_id = sct.organization_id AND cv.id = sct.conversation_id AND cv.type = ?", domain.ConversationTypeCopilot).
		Join("JOIN organization_identities AS agent_oi ON agent_oi.organization_id = sct.organization_id AND agent_oi.id = sct.agent_identity_id").
		Join("JOIN agents AS agent ON agent.organization_id = sct.organization_id AND agent.identity_id = sct.agent_identity_id").
		Join("JOIN organization_identities AS creator ON creator.organization_id = sct.organization_id AND creator.id = sct.created_by_identity_id").
		Where("sct.organization_id = ? AND sct.served_conversation_id = ?", organizationID, servedConversationID).
		OrderExpr("COALESCE(cv.last_activity_at, sct.created_at) DESC, cv.id DESC")
	if threadID != "" {
		query = query.Where("sct.conversation_id = ?", threadID)
	}
	if err := query.Scan(ctx, &threads); err != nil {
		return nil, fmt.Errorf("load customer copilot threads: %w", err)
	}
	return threads, nil
}

// ensureServiceCopilotThread 创建线程及其 AI 员工参与者，重试时核对线程的固定归属。
func ensureServiceCopilotThread(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, threadID, servedConversationID, agentID, body string) error {
	if found, err := lockServiceCopilotThreadDraft(ctx, tx, identity, threadID, servedConversationID, agentID); err != nil || found {
		return err
	}
	serviceExists, err := tx.NewSelect().TableExpr("service_conversations AS svc").
		Where("svc.organization_id = ? AND svc.conversation_id = ?", identity.Organization.ID, servedConversationID).Exists(ctx)
	if err != nil {
		return fmt.Errorf("check copilot service conversation: %w", err)
	}
	if !serviceExists {
		return conversationaction.ErrConversationNotFound
	}
	agentAvailable, err := tx.NewSelect().TableExpr("agents AS agent").
		Join("JOIN organization_identities AS oi ON oi.organization_id = agent.organization_id AND oi.id = agent.identity_id AND oi.type = ?", domain.OrganizationIdentityTypeAgent).
		Where("agent.organization_id = ? AND agent.identity_id = ? AND agent.status = ?", identity.Organization.ID, agentID, domain.IdentityStatusActive).Exists(ctx)
	if err != nil {
		return fmt.Errorf("check copilot agent: %w", err)
	}
	if !agentAvailable {
		return conversationaction.ErrAgentUnavailable
	}
	subjects, err := conversationaction.EnsureOrganizationIdentityChatSubjects(ctx, tx, identity.Organization.ID, []string{identity.OrganizationIdentity.ID, agentID})
	if err != nil {
		return err
	}
	titleText := conversationTitle(body)
	cv := &servermodels.Conversation{
		ID: threadID, OrganizationID: identity.Organization.ID, Type: string(domain.ConversationTypeCopilot), Status: string(domain.ConversationStatusActive),
		Title: &titleText, CreatedBySubjectID: &subjects[identity.OrganizationIdentity.ID].ID,
	}
	inserted, err := tx.NewInsert().Model(cv).Column("id", "organization_id", "type", "status", "title", "created_by_subject_id").On("CONFLICT (id) DO NOTHING").Exec(ctx)
	if err != nil {
		return fmt.Errorf("create copilot thread conversation: %w", err)
	}
	count, err := inserted.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		found, err := lockServiceCopilotThreadDraft(ctx, tx, identity, threadID, servedConversationID, agentID)
		if err != nil {
			return err
		}
		if !found {
			return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
		}
		return nil
	}
	thread := &servermodels.ServiceCopilotThread{
		ConversationID: threadID, OrganizationID: identity.Organization.ID, ServedConversationID: servedConversationID,
		AgentIdentityID: agentID, CreatedByIdentityID: identity.OrganizationIdentity.ID,
	}
	if _, err := tx.NewInsert().Model(thread).Column("conversation_id", "organization_id", "served_conversation_id", "agent_identity_id", "created_by_identity_id").Exec(ctx); err != nil {
		return fmt.Errorf("create customer copilot thread: %w", err)
	}
	participant := &servermodels.ConversationParticipant{
		ID: uuid.NewV7().String(), OrganizationID: identity.Organization.ID, ConversationID: threadID,
		SubjectID: subjects[agentID].ID, Role: string(domain.ConversationParticipantRoleMember),
	}
	if _, err := tx.NewInsert().Model(participant).Column("id", "organization_id", "conversation_id", "subject_id", "role").Exec(ctx); err != nil {
		return fmt.Errorf("create copilot thread agent participant: %w", err)
	}
	return nil
}

// lockServiceCopilotThreadDraft 锁定已有线程编号并核对首条提问的所属服务会话、AI 员工和创建人。
func lockServiceCopilotThreadDraft(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, threadID, servedConversationID, agentID string) (bool, error) {
	cv, err := chatstate.LockConversation(ctx, tx, identity.Organization.ID, threadID)
	if errors.Is(err, chatstate.ErrConversationNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	matches, err := tx.NewSelect().Model((*servermodels.ServiceCopilotThread)(nil)).
		Where("sct.organization_id = ? AND sct.conversation_id = ?", identity.Organization.ID, threadID).
		Where("sct.served_conversation_id = ? AND sct.agent_identity_id = ? AND sct.created_by_identity_id = ?", servedConversationID, agentID, identity.OrganizationIdentity.ID).
		Exists(ctx)
	if err != nil {
		return false, err
	}
	if !matches || cv.Type != string(domain.ConversationTypeCopilot) {
		return false, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	return true, nil
}

// lockServiceCopilotSendContext 锁定线程后校验所属服务会话与 AI 员工可用，并锁定或创建提问成员的参与关系。
func lockServiceCopilotSendContext(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, threadID string) (internalMessageContext, error) {
	row := internalMessageContext{}
	conversation, err := chatstate.LockConversation(ctx, tx, identity.Organization.ID, threadID)
	if err != nil {
		return row, err
	}
	if conversation.Type != string(domain.ConversationTypeCopilot) || conversation.Status != string(domain.ConversationStatusActive) {
		return row, conversationaction.ErrConversationNotFound
	}
	var thread struct {
		AgentIdentityID string  `bun:"agent_identity_id"`
		AgentRevisionID *string `bun:"agent_revision_id"`
		AgentActive     bool    `bun:"agent_active"`
	}
	err = tx.NewSelect().TableExpr("service_copilot_threads AS sct").
		ColumnExpr("sct.agent_identity_id::text AS agent_identity_id, agent.active_revision_id::text AS agent_revision_id").
		ColumnExpr("agent.status = ? AS agent_active", domain.IdentityStatusActive).
		Join("JOIN service_conversations AS svc ON svc.organization_id = sct.organization_id AND svc.conversation_id = sct.served_conversation_id").
		Join("JOIN agents AS agent ON agent.organization_id = sct.organization_id AND agent.identity_id = sct.agent_identity_id").
		Where("sct.organization_id = ? AND sct.conversation_id = ?", identity.Organization.ID, threadID).
		Scan(ctx, &thread)
	if errors.Is(err, sql.ErrNoRows) {
		return row, conversationaction.ErrConversationNotFound
	}
	if err != nil {
		return row, fmt.Errorf("load copilot thread send context: %w", err)
	}
	if !thread.AgentActive {
		return row, conversationaction.ErrAgentUnavailable
	}
	subjects, err := conversationaction.EnsureOrganizationIdentityChatSubjects(ctx, tx, identity.Organization.ID, []string{identity.OrganizationIdentity.ID})
	if err != nil {
		return row, err
	}
	subject := subjects[identity.OrganizationIdentity.ID]
	participant := &servermodels.ConversationParticipant{}
	err = tx.NewSelect().Model(participant).
		Where("cp.organization_id = ? AND cp.conversation_id = ? AND cp.subject_id = ?", identity.Organization.ID, threadID, subject.ID).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// 成员首次提问时加入线程参与者。
		participant = &servermodels.ConversationParticipant{
			ID: uuid.NewV7().String(), OrganizationID: identity.Organization.ID, ConversationID: threadID,
			SubjectID: subject.ID, Role: string(domain.ConversationParticipantRoleMember),
		}
		if _, err := tx.NewInsert().Model(participant).Column("id", "organization_id", "conversation_id", "subject_id", "role").Exec(ctx); err != nil {
			return row, fmt.Errorf("create copilot thread member participant: %w", err)
		}
	} else if err != nil {
		return row, fmt.Errorf("lock copilot thread member participant: %w", err)
	}
	return internalMessageContext{
		Conversation: conversation, ConversationID: threadID, ParticipantID: participant.ID, SubjectID: subject.ID,
		AgentIdentityID: thread.AgentIdentityID, AgentRevisionID: thread.AgentRevisionID, AgentInputKind: domain.AgentInputKindCopilot,
	}, nil
}
