//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	"github.com/uptrace/bun"
)

type messageReferenceRow struct {
	Type               domain.MessageType               `bun:"type"`
	Visibility         domain.MessageVisibility         `bun:"visibility"`
	Deleted            bool                             `bun:"deleted"`
	MessageID          string                           `bun:"message_id"`
	Body               string                           `bun:"body"`
	ChatSubjectID      *string                          `bun:"chat_subject_id"`
	Kind               *string                          `bun:"kind"`
	SourceID           *string                          `bun:"source_id"`
	DisplayName        *string                          `bun:"display_name"`
	ContactNumber      *int64                           `bun:"contact_number"`
	AvatarFileID       *string                          `bun:"avatar_file_id"`
	IdentityType       *domain.OrganizationIdentityType `bun:"identity_type"`
	AssistantOwnerName *string                          `bun:"assistant_owner_name"`
}

// LoadConversationReplyTarget 校验并读取同一会话中的文本或附件引用目标。
func LoadConversationReplyTarget(ctx context.Context, db bun.IDB, organizationID, conversationID, messageID string) (*ConversationMessageReference, error) {
	if messageID == "" {
		return nil, nil
	}
	reference, err := LoadMessageReference(ctx, db, organizationID, conversationID, messageID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && reference.Deleted) {
		return nil, &ConflictError{Reason: ConflictReasonReplyTargetInvalid}
	}
	if err != nil {
		return nil, err
	}
	return reference, nil
}

// LoadMessageReference 读取文本或附件消息引用，并将软删除表示为不可用摘要。
func LoadMessageReference(ctx context.Context, db bun.IDB, organizationID, conversationID, messageID string) (*ConversationMessageReference, error) {
	row := messageReferenceRow{}
	err := db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("msg.id AS message_id, msg.type, msg.visibility").
		ColumnExpr("? AS body", messagequery.Summary("msg")).
		ColumnExpr("msg.deleted_at IS NOT NULL AS deleted").
		ColumnExpr("cs.id AS chat_subject_id").
		ColumnExpr("cs.kind AS kind").
		ColumnExpr("cs.source_id AS source_id").
		ColumnExpr("CASE WHEN cs.kind = ? THEN "+contactname.Expr("c", "cci.display_name")+" ELSE oi.display_name END AS display_name", domain.ChatSubjectKindContact).
		ColumnExpr("c.number AS contact_number").
		ColumnExpr("CASE WHEN cs.kind = ? THEN cci.avatar_file_id ELSE oi.avatar_file_id END AS avatar_file_id", domain.ChatSubjectKindContact).
		ColumnExpr("oi.type AS identity_type").
		ColumnExpr("? AS assistant_owner_name", AssistantOwnerName("oi")).
		Join("LEFT JOIN conversation_participants AS cp ON cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id AND cp.id = msg.sender_participant_id").
		Join("LEFT JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id").
		Join("LEFT JOIN organization_identities AS oi ON oi.organization_id = cs.organization_id AND oi.id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.organization_id = msg.organization_id").
		Join("LEFT JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id AND cci.contact_id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN contacts AS c ON c.id = cs.source_id AND c.organization_id = cs.organization_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Where("msg.organization_id = ?", organizationID).
		Where("msg.conversation_id = ?", conversationID).
		Where("msg.id = ?", messageID).
		Where("msg.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment).
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	if row.Deleted {
		return &ConversationMessageReference{ID: row.MessageID, Type: row.Type, Visibility: row.Visibility, Deleted: true}, nil
	}
	if row.ChatSubjectID == nil || row.Kind == nil || row.SourceID == nil {
		return nil, ErrDataInvariant
	}
	return &ConversationMessageReference{
		ID: row.MessageID, Type: row.Type, Visibility: row.Visibility, Body: row.Body,
		Sender: &ConversationMessageSender{
			ChatSubjectID: *row.ChatSubjectID, Kind: domain.ChatSubjectKind(*row.Kind),
			SourceID: *row.SourceID, DisplayName: row.DisplayName, ContactNumber: row.ContactNumber, AvatarFileID: row.AvatarFileID, IdentityType: row.IdentityType,
			AssistantOwnerName: row.AssistantOwnerName,
		},
	}, nil
}
