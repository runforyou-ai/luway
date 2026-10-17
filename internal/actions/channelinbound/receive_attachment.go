//go:build server

package channelinbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

func newResult(session *servermodels.ServiceSession, channelIdentityID string, message *servermodels.Message, inserted bool) Result {
	openedSession := session.OpeningMessageID == message.ID
	return Result{
		Session: session, Message: message,
		ChannelIdentityID:    channelIdentityID,
		Inserted:             inserted,
		CreatedConversation:  openedSession && session.Sequence == 1,
		OpenedServiceSession: openedSession,
	}
}

// lockInboundAttachmentFile 锁定该渠道访客上传的有效文件，按渠道入站上限校验字节数后激活。
func lockInboundAttachmentFile(ctx context.Context, db bun.IDB, channel *servermodels.Channel, channelIdentityID string, attachment UploadedFile) (*Attachment, error) {
	file := &servermodels.File{}
	err := db.NewSelect().Model(file).ColumnExpr("f.*").ColumnExpr("f.expires_at <= now() AS expired").
		Where("f.id = ? AND f.workspace_id = ?", attachment.FileID, channel.WorkspaceID).
		Where("f.purpose = ? AND f.uploader_channel_identity_id = ?", domain.FilePurposeMessageAttachment, channelIdentityID).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fileaction.ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock inbound attachment file: %w", err)
	}
	if file.Status != string(domain.FileStatusUploaded) || file.Expired {
		return nil, fileaction.ErrFileNotFound
	}
	if limit := domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).InboundAttachmentLimit; file.ByteSize > limit {
		return nil, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonAttachmentTooLarge}
	}
	if _, err := db.NewUpdate().Model(file).Set("status = ?", domain.FileStatusActive).Set("expires_at = NULL").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("activate inbound attachment file: %w", err)
	}
	return &Attachment{
		MessageAttachment: conversationaction.MessageAttachment{
			ID: file.ID, Name: file.OriginalName, ContentType: file.ContentType, ByteSize: file.ByteSize,
			ImageWidth: attachment.ImageWidth, ImageHeight: attachment.ImageHeight, TransferStatus: domain.MessageAttachmentTransferReady,
		},
		StorageKey: file.StorageKey,
	}, nil
}

// createExternalMediaAttachment 为外部平台媒体建立取回中的文件记录，超过渠道入站上限的媒体直接落为取回失败。
func createExternalMediaAttachment(ctx context.Context, db bun.IDB, channel *servermodels.Channel, media ExternalMedia) (*Attachment, error) {
	attachment := &Attachment{MessageAttachment: conversationaction.MessageAttachment{
		Name: media.FileName, ContentType: media.ContentType, ByteSize: media.ByteSize,
		ImageWidth: media.ImageWidth, ImageHeight: media.ImageHeight, TransferStatus: domain.MessageAttachmentTransferFailed,
	}}
	if limit := domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).InboundAttachmentLimit; media.ByteSize > limit {
		return attachment, nil
	}
	file, err := fileaction.CreateExternalAttachment(ctx, db, channel.WorkspaceID, channel.CreatedByUserID, media.ExternalID, fileaction.UploadInput{
		Purpose: domain.FilePurposeMessageAttachment, FileName: media.FileName, ContentType: media.ContentType, ByteSize: media.ByteSize,
	})
	if err != nil {
		return nil, err
	}
	attachment.ID, attachment.Name, attachment.ContentType = file.ID, file.OriginalName, file.ContentType
	attachment.TransferStatus = domain.MessageAttachmentTransferPending
	attachment.StorageKey = file.StorageKey
	return attachment, nil
}
