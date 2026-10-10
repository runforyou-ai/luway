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
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
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
	enqueuer  servertask.TxEnqueuer
	scheduler conversationaction.AgentChatMessageScheduler
}

// NewSendFirstServiceCopilotMessageAction 创建 Copilot 线程首条提问操作。
func NewSendFirstServiceCopilotMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer, scheduler conversationaction.AgentChatMessageScheduler) *SendFirstServiceCopilotMessageAction {
	return &SendFirstServiceCopilotMessageAction{db: db, enqueuer: enqueuer, scheduler: scheduler}
}

// Execute 按线程稳定编号创建线程并幂等保存首条提问。
func (a *SendFirstServiceCopilotMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input FirstServiceCopilotMessageInput) (FirstServiceCopilotMessageResult, error) {
	messageInput := normalizeInternalMessageInput(InternalTextMessageInput{ConversationID: input.ThreadID, ClientMessageID: input.ClientMessageID, Body: input.Body})
	servedConversationID, _ := str.NormalizeUUID(input.ServedConversationID)
	agentID, _ := str.NormalizeUUID(input.AgentIdentityID)
	var result FirstServiceCopilotMessageResult
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
			if err := lockDraftOwnership(ctx, tx, identity, messageInput.ConversationID, agentID, servedConversationID); err != nil {
				return err
			}
			result.Message = saved
			result.Thread, err = loadServiceCopilotThread(ctx, tx, identity.Workspace.ID, servedConversationID, messageInput.ConversationID)
			return err
		}
		if err := ensureServiceCopilotThread(ctx, tx, identity, messageInput.ConversationID, servedConversationID, agentID, messageInput.Body); err != nil {
			return err
		}
		sendContext, err := lockServiceCopilotSendContext(ctx, tx, identity, messageInput.ConversationID)
		if err != nil {
			return err
		}
		result.Message, err = saveInternalMessage(ctx, tx, a.enqueuer, identity, messageInput.message(), sendContext, a.scheduler)
		if err != nil {
			return err
		}
		result.Thread, err = loadServiceCopilotThread(ctx, tx, identity.Workspace.ID, servedConversationID, messageInput.ConversationID)
		return err
	})
	if err != nil {
		return FirstServiceCopilotMessageResult{}, err
	}
	return result, nil
}

// SendServiceCopilotTextMessageAction 向已有 Copilot 线程发送成员提问。
type SendServiceCopilotTextMessageAction struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	scheduler conversationaction.AgentChatMessageScheduler
}

// NewSendServiceCopilotTextMessageAction 创建 Copilot 线程提问操作。
func NewSendServiceCopilotTextMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer, scheduler conversationaction.AgentChatMessageScheduler) *SendServiceCopilotTextMessageAction {
	return &SendServiceCopilotTextMessageAction{db: db, enqueuer: enqueuer, scheduler: scheduler}
}

// Execute 校验线程归属与 AI 员工可用后，在事务中保存提问及执行输入。
func (a *SendServiceCopilotTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input InternalTextMessageInput) (conversationaction.ConversationMessage, error) {
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
		sendContext, err := lockServiceCopilotSendContext(ctx, tx, identity, normalized.ConversationID)
		if err != nil {
			return err
		}
		result, err = saveInternalMessage(ctx, tx, a.enqueuer, identity, normalized.message(), sendContext, a.scheduler)
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
	var threads []ServiceCopilotThread
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := conversationaccess.RequireReadable(ctx, tx, identity, servedConversationID); err != nil {
			return err
		}
		var err error
		threads, err = loadServiceCopilotThreads(ctx, tx, identity.Workspace.ID, servedConversationID, "")
		return err
	})
	if err != nil {
		return nil, err
	}
	return threads, nil
}

// loadServiceCopilotThread 读取服务会话中刚创建或发送的 Copilot 线程。
func loadServiceCopilotThread(ctx context.Context, db bun.IDB, workspaceID, servedConversationID, threadID string) (ServiceCopilotThread, error) {
	threads, err := loadServiceCopilotThreads(ctx, db, workspaceID, servedConversationID, threadID)
	if err != nil {
		return ServiceCopilotThread{}, err
	}
	if len(threads) != 1 {
		return ServiceCopilotThread{}, conversationaction.ErrDataInvariant
	}
	return threads[0], nil
}

// loadServiceCopilotThreads 读取服务会话的线程摘要，threadID 非空时只读取该线程。
func loadServiceCopilotThreads(ctx context.Context, db bun.IDB, workspaceID, servedConversationID, threadID string) ([]ServiceCopilotThread, error) {
	threads := make([]ServiceCopilotThread, 0)
	query := db.NewSelect().
		TableExpr("service_copilot_threads AS sct").
		ColumnExpr("cv.id::text AS id, COALESCE(cv.title, '') AS title").
		ColumnExpr("sct.agent_identity_id::text AS agent_identity_id, agent_oi.display_name AS agent_name, agent_oi.avatar_file_id::text AS agent_avatar_file_id, agent.status AS agent_status").
		ColumnExpr("sct.created_by_identity_id::text AS created_by_identity_id, creator.display_name AS created_by_name").
		ColumnExpr("sct.created_at, COALESCE(cv.last_activity_at, sct.created_at) AS last_activity_at").
		Join("JOIN conversations AS cv ON cv.workspace_id = sct.workspace_id AND cv.id = sct.conversation_id AND cv.type = ?", domain.ConversationTypeCopilot).
		Join("JOIN workspace_identities AS agent_oi ON agent_oi.workspace_id = sct.workspace_id AND agent_oi.id = sct.agent_identity_id").
		Join("JOIN agents AS agent ON agent.workspace_id = sct.workspace_id AND agent.identity_id = sct.agent_identity_id").
		Join("JOIN workspace_identities AS creator ON creator.workspace_id = sct.workspace_id AND creator.id = sct.created_by_identity_id").
		Where("sct.workspace_id = ? AND sct.served_conversation_id = ?", workspaceID, servedConversationID).
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
		Where("svc.workspace_id = ? AND svc.conversation_id = ?", identity.Workspace.ID, servedConversationID).Exists(ctx)
	if err != nil {
		return fmt.Errorf("check copilot service conversation: %w", err)
	}
	if !serviceExists {
		return conversationaction.ErrConversationNotFound
	}
	agentAvailable, err := tx.NewSelect().TableExpr("agents AS agent").
		Join("JOIN workspace_identities AS oi ON oi.workspace_id = agent.workspace_id AND oi.id = agent.identity_id AND oi.type = ?", domain.WorkspaceIdentityTypeAgent).
		Where("agent.workspace_id = ? AND agent.identity_id = ? AND agent.status = ?", identity.Workspace.ID, agentID, domain.IdentityStatusActive).
		Where("NOT ? = ANY(agent.service_audiences)", domain.ServiceAudiencePersonal).Exists(ctx)
	if err != nil {
		return fmt.Errorf("check copilot agent: %w", err)
	}
	if !agentAvailable {
		return conversationaction.ErrAgentUnavailable
	}
	subjects, err := chatstate.EnsureSubjects(ctx, tx, identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, []string{identity.WorkspaceIdentity.ID, agentID})
	if err != nil {
		return err
	}
	titleText := conversationTitle(body)
	cv := &servermodels.Conversation{
		ID: threadID, WorkspaceID: identity.Workspace.ID, Type: string(domain.ConversationTypeCopilot), Status: string(domain.ConversationStatusActive),
		Title: &titleText, CreatedBySubjectID: &subjects[identity.WorkspaceIdentity.ID].ID,
	}
	inserted, err := tx.NewInsert().Model(cv).Column("id", "workspace_id", "type", "status", "title", "created_by_subject_id").On("CONFLICT (id) DO NOTHING").Exec(ctx)
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
		ConversationID: threadID, WorkspaceID: identity.Workspace.ID, ServedConversationID: servedConversationID,
		AgentIdentityID: agentID, CreatedByIdentityID: identity.WorkspaceIdentity.ID,
	}
	if _, err := tx.NewInsert().Model(thread).Column("conversation_id", "workspace_id", "served_conversation_id", "agent_identity_id", "created_by_identity_id").Exec(ctx); err != nil {
		return fmt.Errorf("create customer copilot thread: %w", err)
	}
	participant := &servermodels.ConversationParticipant{
		ID: uuid.NewV7().String(), WorkspaceID: identity.Workspace.ID, ConversationID: threadID,
		SubjectID: subjects[agentID].ID, Role: string(domain.ConversationParticipantRoleMember),
	}
	if _, err := tx.NewInsert().Model(participant).Column("id", "workspace_id", "conversation_id", "subject_id", "role").Exec(ctx); err != nil {
		return fmt.Errorf("create copilot thread agent participant: %w", err)
	}
	return nil
}

// lockServiceCopilotThreadDraft 锁定已有线程编号并核对首条提问的所属服务会话、AI 员工和创建人。
func lockServiceCopilotThreadDraft(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, threadID, servedConversationID, agentID string) (bool, error) {
	cv, err := chatstate.LockConversation(ctx, tx, identity.Workspace.ID, threadID)
	if errors.Is(err, chatstate.ErrConversationNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	matches, err := tx.NewSelect().Model((*servermodels.ServiceCopilotThread)(nil)).
		Where("sct.workspace_id = ? AND sct.conversation_id = ?", identity.Workspace.ID, threadID).
		Where("sct.served_conversation_id = ? AND sct.agent_identity_id = ? AND sct.created_by_identity_id = ?", servedConversationID, agentID, identity.WorkspaceIdentity.ID).
		Exists(ctx)
	if err != nil {
		return false, err
	}
	if !matches || cv.Type != string(domain.ConversationTypeCopilot) {
		return false, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	return true, nil
}

// lockServiceCopilotSendContext 锁定线程后校验所属服务会话与 AI 员工可用，并取得或创建提问成员的参与关系。
func lockServiceCopilotSendContext(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, threadID string) (internalMessageContext, error) {
	row := internalMessageContext{}
	conversation, err := chatstate.LockConversation(ctx, tx, identity.Workspace.ID, threadID)
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
		Join("JOIN service_conversations AS svc ON svc.workspace_id = sct.workspace_id AND svc.conversation_id = sct.served_conversation_id").
		Join("JOIN agents AS agent ON agent.workspace_id = sct.workspace_id AND agent.identity_id = sct.agent_identity_id").
		Where("sct.workspace_id = ? AND sct.conversation_id = ?", identity.Workspace.ID, threadID).
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
	subjects, err := chatstate.EnsureSubjects(ctx, tx, identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, []string{identity.WorkspaceIdentity.ID})
	if err != nil {
		return row, err
	}
	subject := subjects[identity.WorkspaceIdentity.ID]
	// 成员首次提问时加入线程参与者。
	participant, err := chatstate.EnsureParticipant(ctx, tx, identity.Workspace.ID, threadID, subject.ID, uuid.NewV7().String())
	if err != nil {
		return row, err
	}
	return internalMessageContext{
		Conversation: conversation, ConversationID: threadID, ParticipantID: participant.ID, SubjectID: subject.ID,
		AgentIdentityID: thread.AgentIdentityID, AgentRevisionID: thread.AgentRevisionID, AgentInputKind: domain.AgentInputKindCopilot,
	}, nil
}
