//go:build server

package membersend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Draft 是一条成员消息写入前的基础字段。
type Draft struct {
	ConversationID  string
	ParticipantID   string
	ClientMessageID string
	Type            domain.MessageType
	Visibility      domain.MessageVisibility
	Body            string
	// OriginatedAt 为零时由消息追加取持有会话锁之后的数据库时刻。
	OriginatedAt time.Time
}

// NewMessage 构造带新编号与幂等键的成员消息。
func NewMessage(identity *servermodels.Identity, draft Draft) *servermodels.Message {
	key := Key(identity, draft.ClientMessageID)
	visibility := draft.Visibility
	if visibility == "" {
		visibility = domain.MessageVisibilityShared
	}
	return &servermodels.Message{
		ID: uuid.NewV7().String(), WorkspaceID: identity.Workspace.ID, ConversationID: draft.ConversationID,
		SenderParticipantID: &draft.ParticipantID, Type: string(draft.Type), Visibility: string(visibility), Body: draft.Body,
		ClientMessageID: &draft.ClientMessageID, IdempotencyKey: &key, OriginatedAt: draft.OriginatedAt,
	}
}

// ActivateAttachment 锁定本人上传、未过期的消息附件文件并激活，返回待写入消息的附件信息。
func ActivateAttachment(ctx context.Context, tx bun.IDB, identity *servermodels.Identity, attachment AttachmentIntent) (*conversationaction.MessageAttachment, error) {
	file := &servermodels.File{}
	err := tx.NewSelect().Model(file).ColumnExpr("f.*").ColumnExpr("f.expires_at <= now() AS expired").
		Where("f.id = ? AND f.workspace_id = ? AND f.created_by_user_id = ?", attachment.FileID, identity.Workspace.ID, identity.User.ID).
		Where("f.purpose = ? AND f.uploader_channel_identity_id IS NULL", domain.FilePurposeMessageAttachment).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fileaction.ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock message attachment file: %w", err)
	}
	if file.Status != string(domain.FileStatusUploaded) || file.Expired {
		return nil, fileaction.ErrFileNotFound
	}
	if _, err := tx.NewUpdate().Model(file).Set("status = ?", domain.FileStatusActive).Set("expires_at = NULL").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("activate message attachment file: %w", err)
	}
	return &conversationaction.MessageAttachment{
		ID: file.ID, Name: file.OriginalName, ContentType: file.ContentType, ByteSize: file.ByteSize,
		ImageWidth: attachment.ImageWidth, ImageHeight: attachment.ImageHeight, TransferStatus: domain.MessageAttachmentTransferReady,
	}, nil
}

// MarkSenderRead 把发送者在会话中的阅读水位推进到刚发送的消息。
func MarkSenderRead(ctx context.Context, db bun.IDB, identity *servermodels.Identity, message *servermodels.Message) error {
	return conversationaction.AdvanceConversationUserReadState(ctx, db, &servermodels.ConversationUserState{
		WorkspaceID: identity.Workspace.ID, ConversationID: message.ConversationID,
		UserID: identity.User.ID, LastReadMessageID: &message.ID,
	}, message)
}
