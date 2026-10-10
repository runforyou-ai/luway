//go:build server

package directchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// SendFirstDirectTextMessageAction 发送首条单聊消息并按需创建长期会话。
type SendFirstDirectTextMessageAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// FindDirectConversationQuery 按目标身份查找当前成员的活跃长期单聊。
type FindDirectConversationQuery struct {
	db *bun.DB
}

// SendDirectTextMessageAction 持久化企业成员内部单聊文本消息。
type SendDirectTextMessageAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// directTargetRow 是可发起单聊的目标身份。
type directTargetRow struct {
	IdentityID   string                       `bun:"identity_id"`
	IdentityType domain.WorkspaceIdentityType `bun:"identity_type"`
	DisplayName  string                       `bun:"display_name"`
	AvatarFileID *string                      `bun:"avatar_file_id"`
}

// directConversationSummaryRow 是单聊列表摘要的一行。
type directConversationSummaryRow struct {
	LastActivityAt            *time.Time                    `bun:"last_activity_at"`
	ID                        string                        `bun:"id"`
	Preview                   *string                       `bun:"preview"`
	PreviewSenderIdentityType *domain.WorkspaceIdentityType `bun:"preview_sender_identity_type"`
	LastMessageAt             *time.Time                    `bun:"last_message_at"`
}

// NewSendFirstDirectTextMessageAction 创建首条单聊消息发送操作。
func NewSendFirstDirectTextMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *SendFirstDirectTextMessageAction {
	return &SendFirstDirectTextMessageAction{db: db, enqueuer: enqueuer}
}

// NewFindDirectConversationQuery 创建内部单聊查找查询。
func NewFindDirectConversationQuery(db *bun.DB) *FindDirectConversationQuery {
	return &FindDirectConversationQuery{db: db}
}

// Execute 返回当前成员与目标身份的活跃长期单聊。
func (q *FindDirectConversationQuery) Execute(ctx context.Context, identity *servermodels.Identity, targetIdentityID string) (*DirectConversationSummary, error) {
	targetIdentityID, _ = str.NormalizeUUID(targetIdentityID)
	if targetIdentityID == identity.WorkspaceIdentity.ID {
		return nil, conversationaction.ErrDirectTargetNotFound
	}
	target, err := loadDirectTarget(ctx, q.db, identity.Workspace.ID, targetIdentityID)
	if err != nil {
		return nil, err
	}
	conversation, err := findDirectConversation(ctx, q.db, identity.Workspace.ID, identity.WorkspaceIdentity.ID, targetIdentityID)
	if err != nil || conversation == nil || conversation.Status != string(domain.ConversationStatusActive) {
		return nil, err
	}
	summary, err := loadDirectConversationSummary(ctx, q.db, identity.Workspace.ID, conversation.ID, target)
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

// NewSendDirectTextMessageAction 创建内部单聊发送操作。
func NewSendDirectTextMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *SendDirectTextMessageAction {
	return &SendDirectTextMessageAction{db: db, enqueuer: enqueuer}
}

// Execute 发送首条单聊消息并按需创建当前成员与目标成员的长期单聊。
func (a *SendFirstDirectTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input FirstDirectTextMessageInput) (FirstDirectTextMessageResult, error) {
	targetIdentityID, _ := str.NormalizeUUID(input.TargetIdentityID)
	clientMessageID, _ := str.NormalizeUUID(input.ClientMessageID)
	body := strings.TrimSpace(input.Body)
	if targetIdentityID == identity.WorkspaceIdentity.ID {
		return FirstDirectTextMessageResult{}, conversationaction.ErrDirectTargetNotFound
	}
	message := internalMessage{ClientMessageID: clientMessageID, Body: body}
	var result FirstDirectTextMessageResult
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, map[string]struct{}{
		serverstorage.UniqueDirectConversationPair: {},
	}, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 发送资格校验前先按发送编号读取已有单聊中本人保存的消息。
		existing, err := findDirectConversation(ctx, tx, identity.Workspace.ID, identity.WorkspaceIdentity.ID, targetIdentityID)
		if err != nil {
			return err
		}
		if existing != nil {
			saved, found, err := replayInternalMessage(ctx, tx, identity, internalMessage{ConversationID: existing.ID, ClientMessageID: clientMessageID, Body: body})
			if err != nil {
				return err
			}
			if found {
				result, err = firstDirectResult(ctx, tx, identity, existing.ID, targetIdentityID, saved)
				return err
			}
		}
		target, err := loadDirectTarget(ctx, tx, identity.Workspace.ID, targetIdentityID)
		if err != nil {
			return err
		}
		conversation, err := findOrCreateDirectConversation(ctx, tx, identity.Workspace.ID, identity.WorkspaceIdentity.ID, target.IdentityID)
		if err != nil {
			return err
		}
		message.ConversationID = conversation.ID
		saved, err := sendDirectMessage(ctx, tx, a.enqueuer, identity, message, true)
		if err != nil {
			return err
		}
		result, err = firstDirectResult(ctx, tx, identity, conversation.ID, targetIdentityID, saved)
		return err
	})
	if err != nil {
		return FirstDirectTextMessageResult{}, err
	}
	return result, nil
}

// firstDirectResult 组装首发单聊的会话摘要与消息。
func firstDirectResult(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID, targetIdentityID string, message conversationaction.ConversationMessage) (FirstDirectTextMessageResult, error) {
	peer, err := loadDirectPeer(ctx, db, identity.Workspace.ID, targetIdentityID)
	if err != nil {
		return FirstDirectTextMessageResult{}, err
	}
	summary, err := loadDirectConversationSummary(ctx, db, identity.Workspace.ID, conversationID, peer)
	if err != nil {
		return FirstDirectTextMessageResult{}, err
	}
	return FirstDirectTextMessageResult{Conversation: summary, Message: message}, nil
}

// Execute 在事务中写入内部单聊文本消息。
func (a *SendDirectTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input InternalTextMessageInput) (conversationaction.ConversationMessage, error) {
	normalized := normalizeInternalMessageInput(input)
	message := normalized.message()
	var result conversationaction.ConversationMessage
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 发送资格校验前先按发送编号读取本人保存的消息。
		saved, found, err := replayInternalMessage(ctx, tx, identity, message)
		if err != nil || found {
			result = saved
			return err
		}
		result, err = sendDirectMessage(ctx, tx, a.enqueuer, identity, message, false)
		return err
	})
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	return result, nil
}

// sendDirectMessage 锁定真人单聊、按显式首发意图恢复归档会话并保存消息。
func sendDirectMessage(ctx context.Context, tx bun.Tx, enqueuer servertask.TxEnqueuer, identity *servermodels.Identity, input internalMessage, restoreArchived bool) (conversationaction.ConversationMessage, error) {
	member, err := chatstate.LockMember(ctx, tx, identity, input.ConversationID)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	conversation := member.Conversation
	if conversation.Type != string(domain.ConversationTypeDirect) {
		return conversationaction.ConversationMessage{}, conversationaction.ErrConversationNotFound
	}
	if restoreArchived {
		if err := reactivateDirectConversation(ctx, tx, conversation); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
	}
	// 等待会话锁后重新读取目标资格。
	sendContext, err := loadDirectSendContext(ctx, tx, identity, input.ConversationID)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	sendContext.Conversation = conversation
	return saveInternalMessage(ctx, tx, enqueuer, identity, input, sendContext, nil)
}

// loadDirectTarget 读取同企业可发起单聊的活跃成员身份。
func loadDirectTarget(ctx context.Context, db bun.IDB, workspaceID, identityID string) (directTargetRow, error) {
	return scanDirectPeer(ctx, directPeerQuery(db, workspaceID, identityID).Where("u.status = ?", domain.IdentityStatusActive))
}

// loadDirectPeer 读取单聊对端成员身份，不要求对端仍然活跃。
func loadDirectPeer(ctx context.Context, db bun.IDB, workspaceID, identityID string) (directTargetRow, error) {
	return scanDirectPeer(ctx, directPeerQuery(db, workspaceID, identityID))
}

// directPeerQuery 构造读取同企业真人成员身份的查询。
func directPeerQuery(db bun.IDB, workspaceID, identityID string) *bun.SelectQuery {
	return db.NewSelect().
		TableExpr("workspace_identities AS oi").
		ColumnExpr("oi.id AS identity_id").
		ColumnExpr("oi.type AS identity_type").
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
		Join("LEFT JOIN users AS u ON u.workspace_id = oi.workspace_id AND u.identity_id = oi.id").
		Where("oi.workspace_id = ?", workspaceID).
		Where("oi.id = ?", identityID).
		Where("oi.type = ?", domain.WorkspaceIdentityTypeUser)
}

// scanDirectPeer 执行单聊成员身份查询，未找到时返回单聊目标不存在。
func scanDirectPeer(ctx context.Context, query *bun.SelectQuery) (directTargetRow, error) {
	row := directTargetRow{}
	err := query.Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return directTargetRow{}, conversationaction.ErrDirectTargetNotFound
	}
	if err != nil {
		return directTargetRow{}, fmt.Errorf("load direct target: %w", err)
	}
	return row, nil
}

// normalizeDirectIdentityPair 按稳定顺序排列单聊双方身份。
func normalizeDirectIdentityPair(firstIdentityID, secondIdentityID string) (string, string) {
	identityIDs := []string{firstIdentityID, secondIdentityID}
	sort.Strings(identityIDs)
	return identityIDs[0], identityIDs[1]
}

// findOrCreateDirectConversation 查找当前成员与目标成员的长期单聊，不存在时创建。
func findOrCreateDirectConversation(ctx context.Context, db bun.IDB, workspaceID, currentIdentityID, targetIdentityID string) (*servermodels.Conversation, error) {
	conversation, err := findDirectConversation(ctx, db, workspaceID, currentIdentityID, targetIdentityID)
	if err != nil || conversation != nil {
		return conversation, err
	}
	return createDirectConversation(ctx, db, workspaceID, currentIdentityID, targetIdentityID)
}

// reactivateDirectConversation 把已归档的单聊恢复为进行中，用于显式向目标成员首发。
func reactivateDirectConversation(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation) error {
	if conversation.Status != string(domain.ConversationStatusArchived) {
		return nil
	}
	if _, err := db.NewUpdate().Model(conversation).
		Set("status = ?", domain.ConversationStatusActive).WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("reactivate direct conversation: %w", err)
	}
	return nil
}

// findDirectConversation 查找规范身份对唯一的长期单聊。
func findDirectConversation(ctx context.Context, db bun.IDB, workspaceID, firstIdentityID, secondIdentityID string) (*servermodels.Conversation, error) {
	firstIdentityID, secondIdentityID = normalizeDirectIdentityPair(firstIdentityID, secondIdentityID)
	conversation := &servermodels.Conversation{}
	err := db.NewSelect().Model(conversation).
		Join("JOIN direct_conversations AS dc ON dc.workspace_id = cv.workspace_id AND dc.conversation_id = cv.id").
		Where("cv.workspace_id = ?", workspaceID).
		Where("cv.type = ?", domain.ConversationTypeDirect).
		Where("dc.first_identity_id = ?", firstIdentityID).
		Where("dc.second_identity_id = ?", secondIdentityID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find direct conversation: %w", err)
	}
	return conversation, nil
}

// createDirectConversation 创建内部单聊和双方参与者。
func createDirectConversation(ctx context.Context, db bun.IDB, workspaceID, currentIdentityID, targetIdentityID string) (*servermodels.Conversation, error) {
	subjects, err := chatstate.EnsureSubjects(ctx, db, workspaceID, domain.ChatSubjectKindWorkspaceIdentity, []string{currentIdentityID, targetIdentityID})
	if err != nil {
		return nil, err
	}
	currentSubjectID, targetSubjectID := subjects[currentIdentityID].ID, subjects[targetIdentityID].ID
	conversation := &servermodels.Conversation{
		ID: uuid.NewV7().String(), WorkspaceID: workspaceID,
		Type: string(domain.ConversationTypeDirect), Status: string(domain.ConversationStatusActive),
		CreatedBySubjectID: &currentSubjectID,
	}
	if _, err := db.NewInsert().Model(conversation).
		Column("id", "workspace_id", "type", "status", "created_by_subject_id").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create direct conversation: %w", err)
	}
	firstIdentityID, secondIdentityID := normalizeDirectIdentityPair(currentIdentityID, targetIdentityID)
	relation := &servermodels.DirectConversation{
		ConversationID: conversation.ID, WorkspaceID: workspaceID,
		FirstIdentityID: firstIdentityID, SecondIdentityID: secondIdentityID,
	}
	if _, err := db.NewInsert().Model(relation).
		Column("conversation_id", "workspace_id", "first_identity_id", "second_identity_id").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create direct conversation relation: %w", err)
	}
	participants := []*servermodels.ConversationParticipant{
		{ID: uuid.NewV7().String(), WorkspaceID: workspaceID, ConversationID: conversation.ID, SubjectID: currentSubjectID, Role: string(domain.ConversationParticipantRoleMember)},
		{ID: uuid.NewV7().String(), WorkspaceID: workspaceID, ConversationID: conversation.ID, SubjectID: targetSubjectID, Role: string(domain.ConversationParticipantRoleMember)},
	}
	if _, err := db.NewInsert().Model(&participants).
		Column("id", "workspace_id", "conversation_id", "subject_id", "role").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create direct conversation participants: %w", err)
	}
	return conversation, nil
}

// loadDirectConversationSummary 读取单聊当前摘要。
func loadDirectConversationSummary(ctx context.Context, db bun.IDB, workspaceID, conversationID string, target directTargetRow) (DirectConversationSummary, error) {
	row := directConversationSummaryRow{}
	err := db.NewSelect().
		TableExpr("conversations AS cv").
		ColumnExpr("cv.id AS id").
		ColumnExpr("? AS preview", messagequery.Summary("msg")).
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("cv.last_message_at AS last_message_at").
		ColumnExpr("cv.last_activity_at AS last_activity_at").
		Join("LEFT JOIN messages AS msg ON msg.workspace_id = cv.workspace_id AND msg.conversation_id = cv.id AND msg.id = cv.last_message_id AND msg.deleted_at IS NULL").
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = msg.sender_participant_id AND preview_cp.workspace_id = msg.workspace_id AND preview_cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.workspace_id = preview_cp.workspace_id").
		Join("LEFT JOIN workspace_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.workspace_id = preview_cs.workspace_id AND preview_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Where("cv.workspace_id = ?", workspaceID).
		Where("cv.id = ?", conversationID).
		Where("cv.type = ?", domain.ConversationTypeDirect).
		Scan(ctx, &row)
	if err != nil {
		return DirectConversationSummary{}, fmt.Errorf("load direct conversation summary: %w", err)
	}
	return DirectConversationSummary{
		ID: row.ID, LastActivityAt: row.LastActivityAt, PeerIdentityID: target.IdentityID, PeerType: target.IdentityType, PeerName: target.DisplayName, PeerAvatarFileID: target.AvatarFileID,
		Preview: row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType, LastMessageAt: row.LastMessageAt,
	}, nil
}

// loadDirectSendContext 校验当前成员是单聊现有有效参与者。
func loadDirectSendContext(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID string) (internalMessageContext, error) {
	row := internalMessageContext{}
	err := db.NewSelect().
		TableExpr("conversations AS cv").
		ColumnExpr("cv.id AS conversation_id").
		ColumnExpr("mine.id AS participant_id").
		ColumnExpr("mine_cs.id AS subject_id").
		Join("JOIN direct_conversations AS dc ON dc.workspace_id = cv.workspace_id AND dc.conversation_id = cv.id").
		Join("JOIN conversation_participants AS mine ON mine.workspace_id = cv.workspace_id AND mine.conversation_id = cv.id AND mine.left_at IS NULL").
		Join("JOIN chat_subjects AS mine_cs ON mine_cs.workspace_id = mine.workspace_id AND mine_cs.id = mine.subject_id AND mine_cs.kind = ? AND mine_cs.source_id = ?", domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID).
		Join("JOIN workspace_identities AS peer_oi ON peer_oi.workspace_id = dc.workspace_id AND peer_oi.id = CASE WHEN dc.first_identity_id = ? THEN dc.second_identity_id ELSE dc.first_identity_id END", identity.WorkspaceIdentity.ID).
		Join("LEFT JOIN users AS peer_u ON peer_u.workspace_id = peer_oi.workspace_id AND peer_u.identity_id = peer_oi.id").
		Where("cv.workspace_id = ?", identity.Workspace.ID).
		Where("cv.id = ?", conversationID).
		Where("cv.type = ?", domain.ConversationTypeDirect).
		Where("cv.status = ?", domain.ConversationStatusActive).
		Where("? IN (dc.first_identity_id, dc.second_identity_id)", identity.WorkspaceIdentity.ID).
		Where("peer_oi.type = ? AND peer_u.status = ?", domain.WorkspaceIdentityTypeUser, domain.IdentityStatusActive).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return internalMessageContext{}, conversationaction.ErrConversationNotFound
	}
	if err != nil {
		return internalMessageContext{}, fmt.Errorf("load direct send context: %w", err)
	}
	return row, nil
}
