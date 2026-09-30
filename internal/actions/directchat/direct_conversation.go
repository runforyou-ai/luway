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
	"unicode/utf8"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// SendFirstDirectTextMessageAction 发送首条单聊消息并按需创建长期会话。
type SendFirstDirectTextMessageAction struct {
	db *bun.DB
}

// FindDirectConversationQuery 按目标身份查找当前成员的活跃长期单聊。
type FindDirectConversationQuery struct {
	db *bun.DB
}

// SendDirectTextMessageAction 持久化企业成员内部单聊文本消息。
type SendDirectTextMessageAction struct {
	db *bun.DB
}

type directTargetRow struct {
	IdentityID   string                          `bun:"identity_id"`
	IdentityType domain.OrganizationIdentityType `bun:"identity_type"`
	DisplayName  string                          `bun:"display_name"`
	AvatarFileID *string                         `bun:"avatar_file_id"`
}

type directConversationSummaryRow struct {
	LastActivityAt            *time.Time                       `bun:"last_activity_at"`
	ID                        string                           `bun:"id"`
	Preview                   *string                          `bun:"preview"`
	PreviewSenderIdentityType *domain.OrganizationIdentityType `bun:"preview_sender_identity_type"`
	LastMessageAt             *time.Time                       `bun:"last_message_at"`
}

// NewSendFirstDirectTextMessageAction 创建首条单聊消息发送操作。
func NewSendFirstDirectTextMessageAction(db *bun.DB) *SendFirstDirectTextMessageAction {
	return &SendFirstDirectTextMessageAction{db: db}
}

// NewFindDirectConversationQuery 创建内部单聊查找查询。
func NewFindDirectConversationQuery(db *bun.DB) *FindDirectConversationQuery {
	return &FindDirectConversationQuery{db: db}
}

// Execute 返回当前成员与目标身份的活跃长期单聊。
func (q *FindDirectConversationQuery) Execute(ctx context.Context, identity *servermodels.Identity, targetIdentityID string) (*DirectConversationSummary, error) {
	targetIdentityID, valid := common.NormalizeUUID(targetIdentityID)
	if !valid || targetIdentityID == identity.OrganizationIdentity.ID {
		return nil, conversationaction.ErrDirectTargetNotFound
	}
	target, err := loadDirectTarget(ctx, q.db, identity.Organization.ID, targetIdentityID)
	if err != nil {
		return nil, err
	}
	conversation, err := findDirectConversation(ctx, q.db, identity.Organization.ID, identity.OrganizationIdentity.ID, targetIdentityID)
	if err != nil || conversation == nil || conversation.Status != string(domain.ConversationStatusActive) {
		return nil, err
	}
	summary, err := loadDirectConversationSummary(ctx, q.db, identity.Organization.ID, conversation.ID, target)
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

// NewSendDirectTextMessageAction 创建内部单聊发送操作。
func NewSendDirectTextMessageAction(db *bun.DB) *SendDirectTextMessageAction {
	return &SendDirectTextMessageAction{db: db}
}

// Execute 发送首条单聊消息并按需创建当前成员与目标成员的长期单聊。
func (a *SendFirstDirectTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input FirstDirectTextMessageInput) (FirstDirectTextMessageResult, error) {
	targetIdentityID, valid := common.NormalizeUUID(input.TargetIdentityID)
	clientMessageID, clientMessageIDValid := common.NormalizeUUID(input.ClientMessageID)
	body := strings.TrimSpace(input.Body)
	fields := map[string]conversationaction.ValidationCode{}
	if !valid {
		fields["targetIdentityId"] = conversationaction.ValidationTargetIdentityIDInvalid
	}
	if !clientMessageIDValid {
		fields["clientMessageId"] = conversationaction.ValidationClientMessageIDInvalid
	}
	if body == "" {
		fields["body"] = conversationaction.ValidationBodyRequired
	} else if utf8.RuneCountInString(body) > conversationaction.MaxMessageBodyRunes {
		fields["body"] = conversationaction.ValidationBodyTooLong
	}
	if len(fields) > 0 {
		return FirstDirectTextMessageResult{}, &conversationaction.ValidationError{Fields: fields}
	}
	if targetIdentityID == identity.OrganizationIdentity.ID {
		return FirstDirectTextMessageResult{}, conversationaction.ErrDirectTargetNotFound
	}
	var result FirstDirectTextMessageResult
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, map[string]struct{}{
		"direct_conversations_organization_identity_pair_unique": {},
	}, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		target, err := loadDirectTarget(ctx, tx, identity.Organization.ID, targetIdentityID)
		if err != nil {
			return err
		}
		conversation, err := findOrCreateDirectConversation(ctx, tx, identity.Organization.ID, identity.OrganizationIdentity.ID, targetIdentityID)
		if err != nil {
			return err
		}
		message, err := sendDirectTextMessage(ctx, tx, identity, InternalTextMessageInput{
			ConversationID: conversation.ID, ClientMessageID: clientMessageID, Body: body,
		}, true)
		if err != nil {
			return err
		}
		summary, err := loadDirectConversationSummary(ctx, tx, identity.Organization.ID, conversation.ID, target)
		if err != nil {
			return err
		}
		result = FirstDirectTextMessageResult{Conversation: summary, Message: message}
		return nil
	})
	if err != nil {
		return FirstDirectTextMessageResult{}, err
	}
	return result, nil
}

// Execute 在事务中写入内部单聊文本消息。
func (a *SendDirectTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input InternalTextMessageInput) (conversationaction.ConversationMessage, error) {
	normalized, fields := normalizeInternalMessageInput(input)
	if len(fields) > 0 {
		return conversationaction.ConversationMessage{}, &conversationaction.ValidationError{Fields: fields}
	}
	var result conversationaction.ConversationMessage
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var sendErr error
		result, sendErr = sendDirectTextMessage(ctx, tx, identity, normalized, false)
		return sendErr
	})
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	return result, nil
}

// sendDirectTextMessage 锁定真人单聊并按显式首发意图恢复归档会话。
func sendDirectTextMessage(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, input InternalTextMessageInput, restoreArchived bool) (conversationaction.ConversationMessage, error) {
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
	// 等待会话锁后重新读取目标资格，幂等重放也需通过当前发送授权。
	sendContext, err := loadDirectSendContext(ctx, tx, identity, input.ConversationID)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	sendContext.Conversation = conversation
	return saveInternalTextMessage(ctx, tx, identity, input, sendContext, nil)
}

// loadDirectTarget 读取同企业可发起单聊的活跃成员身份。
func loadDirectTarget(ctx context.Context, db bun.IDB, organizationID, identityID string) (directTargetRow, error) {
	row := directTargetRow{}
	err := db.NewSelect().
		TableExpr("organization_identities AS oi").
		ColumnExpr("oi.id AS identity_id").
		ColumnExpr("oi.type AS identity_type").
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
		Join("LEFT JOIN users AS u ON u.organization_id = oi.organization_id AND u.identity_id = oi.id").
		Where("oi.organization_id = ?", organizationID).
		Where("oi.id = ?", identityID).
		Where("oi.type = ? AND u.status = ?", domain.OrganizationIdentityTypeUser, domain.IdentityStatusActive).
		Scan(ctx, &row)
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
func findOrCreateDirectConversation(ctx context.Context, db bun.IDB, organizationID, currentIdentityID, targetIdentityID string) (*servermodels.Conversation, error) {
	conversation, err := findDirectConversation(ctx, db, organizationID, currentIdentityID, targetIdentityID)
	if err != nil || conversation != nil {
		return conversation, err
	}
	return createDirectConversation(ctx, db, organizationID, currentIdentityID, targetIdentityID)
}

// reactivateDirectConversation 把已归档的单聊恢复为进行中，用于显式向目标成员首发。
func reactivateDirectConversation(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation) error {
	if conversation.Status != string(domain.ConversationStatusArchived) {
		return nil
	}
	if _, err := db.NewUpdate().Model(conversation).
		Set("status = ?", domain.ConversationStatusActive).
		Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("reactivate direct conversation: %w", err)
	}
	return nil
}

// findDirectConversation 查找规范身份对唯一的长期单聊。
func findDirectConversation(ctx context.Context, db bun.IDB, organizationID, firstIdentityID, secondIdentityID string) (*servermodels.Conversation, error) {
	firstIdentityID, secondIdentityID = normalizeDirectIdentityPair(firstIdentityID, secondIdentityID)
	conversation := &servermodels.Conversation{}
	err := db.NewSelect().Model(conversation).
		Join("JOIN direct_conversations AS dc ON dc.organization_id = cv.organization_id AND dc.conversation_id = cv.id").
		Where("cv.organization_id = ?", organizationID).
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
func createDirectConversation(ctx context.Context, db bun.IDB, organizationID, currentIdentityID, targetIdentityID string) (*servermodels.Conversation, error) {
	subjects, err := conversationaction.EnsureOrganizationIdentityChatSubjects(ctx, db, organizationID, []string{currentIdentityID, targetIdentityID})
	if err != nil {
		return nil, err
	}
	currentSubjectID, targetSubjectID := subjects[currentIdentityID].ID, subjects[targetIdentityID].ID
	conversation := &servermodels.Conversation{
		ID: uuid.NewV7().String(), OrganizationID: organizationID,
		Type: string(domain.ConversationTypeDirect), Status: string(domain.ConversationStatusActive),
		CreatedBySubjectID: &currentSubjectID,
	}
	if _, err := db.NewInsert().Model(conversation).
		Column("id", "organization_id", "type", "status", "created_by_subject_id").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create direct conversation: %w", err)
	}
	firstIdentityID, secondIdentityID := normalizeDirectIdentityPair(currentIdentityID, targetIdentityID)
	relation := &servermodels.DirectConversation{
		ConversationID: conversation.ID, OrganizationID: organizationID,
		FirstIdentityID: firstIdentityID, SecondIdentityID: secondIdentityID,
	}
	if _, err := db.NewInsert().Model(relation).
		Column("conversation_id", "organization_id", "first_identity_id", "second_identity_id").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create direct conversation relation: %w", err)
	}
	participants := []*servermodels.ConversationParticipant{
		{ID: uuid.NewV7().String(), OrganizationID: organizationID, ConversationID: conversation.ID, SubjectID: currentSubjectID, Role: string(domain.ConversationParticipantRoleMember)},
		{ID: uuid.NewV7().String(), OrganizationID: organizationID, ConversationID: conversation.ID, SubjectID: targetSubjectID, Role: string(domain.ConversationParticipantRoleMember)},
	}
	if _, err := db.NewInsert().Model(&participants).
		Column("id", "organization_id", "conversation_id", "subject_id", "role").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create direct conversation participants: %w", err)
	}
	return conversation, nil
}

// loadDirectConversationSummary 读取单聊当前摘要。
func loadDirectConversationSummary(ctx context.Context, db bun.IDB, organizationID, conversationID string, target directTargetRow) (DirectConversationSummary, error) {
	row := directConversationSummaryRow{}
	err := db.NewSelect().
		TableExpr("conversations AS cv").
		ColumnExpr("cv.id AS id").
		ColumnExpr("? AS preview", messagequery.Summary("msg")).
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("cv.last_message_at AS last_message_at").
		ColumnExpr("cv.last_activity_at AS last_activity_at").
		Join("LEFT JOIN messages AS msg ON msg.organization_id = cv.organization_id AND msg.conversation_id = cv.id AND msg.id = cv.last_message_id AND msg.deleted_at IS NULL").
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = msg.sender_participant_id AND preview_cp.organization_id = msg.organization_id AND preview_cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.organization_id = preview_cp.organization_id").
		Join("LEFT JOIN organization_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.organization_id = preview_cs.organization_id AND preview_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Where("cv.organization_id = ?", organizationID).
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
		Join("JOIN direct_conversations AS dc ON dc.organization_id = cv.organization_id AND dc.conversation_id = cv.id").
		Join("JOIN conversation_participants AS mine ON mine.organization_id = cv.organization_id AND mine.conversation_id = cv.id AND mine.left_at IS NULL").
		Join("JOIN chat_subjects AS mine_cs ON mine_cs.organization_id = mine.organization_id AND mine_cs.id = mine.subject_id AND mine_cs.kind = ? AND mine_cs.source_id = ?", domain.ChatSubjectKindOrganizationIdentity, identity.OrganizationIdentity.ID).
		Join("JOIN organization_identities AS peer_oi ON peer_oi.organization_id = dc.organization_id AND peer_oi.id = CASE WHEN dc.first_identity_id = ? THEN dc.second_identity_id ELSE dc.first_identity_id END", identity.OrganizationIdentity.ID).
		Join("LEFT JOIN users AS peer_u ON peer_u.organization_id = peer_oi.organization_id AND peer_u.identity_id = peer_oi.id").
		Where("cv.organization_id = ?", identity.Organization.ID).
		Where("cv.id = ?", conversationID).
		Where("cv.type = ?", domain.ConversationTypeDirect).
		Where("cv.status = ?", domain.ConversationStatusActive).
		Where("? IN (dc.first_identity_id, dc.second_identity_id)", identity.OrganizationIdentity.ID).
		Where("peer_oi.type = ? AND peer_u.status = ?", domain.OrganizationIdentityTypeUser, domain.IdentityStatusActive).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return internalMessageContext{}, conversationaction.ErrConversationNotFound
	}
	if err != nil {
		return internalMessageContext{}, fmt.Errorf("load direct send context: %w", err)
	}
	return row, nil
}
