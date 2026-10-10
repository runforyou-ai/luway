//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestCustomerAttachmentReply 验证成员向网站客户会话发送附件时激活文件、隐式领取周期并按发送意图幂等。
func TestCustomerAttachmentReply(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	send := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, testEnqueuer)
	fileID := uploadedAttachment(t, f.db, f.owner, "报价单.pdf", "application/pdf")
	input := servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID, Body: "请查收报价单",
	}
	message, err := send.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	require.Equal(t, domain.MessageTypeAttachment, message.Type)
	require.NotNil(t, message.Attachment)
	require.Equal(t, fileID, message.Attachment.ID)
	require.Equal(t, "报价单.pdf", message.Attachment.Name)
	require.Equal(t, domain.MessageAttachmentTransferReady, message.Attachment.TransferStatus)
	file := &servermodels.File{ID: fileID}
	require.NoError(t, f.db.NewSelect().Model(file).WherePK().Scan(ctx))
	require.Equal(t, string(domain.FileStatusActive), file.Status, "file not activated")
	require.Nil(t, file.ExpiresAt, "file not activated")
	// 首次回复隐式领取当前客服周期。
	var assignee string
	require.NoError(t, f.db.NewSelect().Table("service_sessions").ColumnExpr("COALESCE(assignee_identity_id::text, '')").
		Where("conversation_id = ?", f.conversationID).Scan(ctx, &assignee))
	require.Equal(t, f.owner.WorkspaceIdentity.ID, assignee)
	replay, err := send.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	require.Equal(t, message.ID, replay.ID)
	require.NotNil(t, replay.Attachment)
	require.Equal(t, fileID, replay.Attachment.ID)
	var conflict *conversationaction.ConflictError
	for _, changed := range []servicesessionaction.ServiceAttachmentMessageInput{
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, FileID: fileID, Body: "改过的说明"},
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, FileID: uuid.NewV7().String(), Body: input.Body},
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, FileID: fileID, Body: input.Body, ImageWidth: 100, ImageHeight: 100},
	} {
		_, err := send.Execute(ctx, f.owner, changed)
		require.ErrorAs(t, err, &conflict, "changed intent accepted")
		require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason, "changed intent accepted")
	}
	// 已发送的附件文件不能再次关联到新消息。
	reuse := servicesessionaction.ServiceAttachmentMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID}
	_, err = send.Execute(ctx, f.owner, reuse)
	require.ErrorIs(t, err, fileaction.ErrFileNotFound, "activated file reused")
	// 历史窗口返回附件元数据与取回状态。
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	last := history.Messages[len(history.Messages)-1]
	require.Equal(t, message.ID, last.ID)
	require.NotNil(t, last.Attachment)
	require.Equal(t, "报价单.pdf", last.Attachment.Name)
	require.Equal(t, domain.MessageAttachmentTransferReady, last.Attachment.TransferStatus)
}

// TestCustomerAttachmentChannelLimits 验证渠道附件能力与说明长度在事务内生效。
func TestCustomerAttachmentChannelLimits(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	send := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, testEnqueuer)
	fileID := uploadedAttachment(t, f.db, f.owner, "说明.txt", "text/plain")
	long := make([]rune, 4001)
	for index := range long {
		long[index] = '鹿'
	}
	var conflict *conversationaction.ConflictError
	// Telegram 渠道按平台的说明上限拒绝发送。
	_, err := f.db.NewUpdate().Table("channels").Set("type = ?", domain.ChannelTypeTelegram).Where("id = ?", f.channelID).Exec(ctx)
	require.NoError(t, err)
	_, err = send.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID, Body: string(long[:domain.ChannelCapabilitiesOf(domain.ChannelTypeTelegram).CaptionLimit+1]),
	})
	require.ErrorAs(t, err, &conflict, "long telegram caption accepted")
	require.Equal(t, servicesessionaction.ConflictReasonCaptionTooLong, conflict.Reason, "long telegram caption accepted")
	// 不支持外发附件的渠道拒绝附件。
	_, err = f.db.NewUpdate().Table("channels").Set("type = ?", domain.ChannelTypeWeComBot).Where("id = ?", f.channelID).Exec(ctx)
	require.NoError(t, err)
	_, err = send.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID,
	})
	require.ErrorAs(t, err, &conflict, "unsupported channel accepted")
	require.Equal(t, servicesessionaction.ConflictReasonChannelAttachmentUnsupported, conflict.Reason, "unsupported channel accepted")
	// 被拒绝的附件不进入外部投递队列。
	count, err := f.db.NewSelect().Table("channel_message_deliveries").Where("conversation_id = ?", f.conversationID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "deliveries")
}

// TestCustomerAttachmentByteLimit 验证超过渠道字节上限的附件在事务内被拒绝且文件保持未激活。
func TestCustomerAttachmentByteLimit(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	send := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, testEnqueuer)
	fileID := uploadedAttachment(t, f.db, f.owner, "超大附件.bin", "application/octet-stream")
	limit, _ := domain.ChannelCapabilitiesOf(domain.ChannelTypeWebsite).AttachmentByteLimit("application/octet-stream")
	_, err := f.db.NewUpdate().Table("files").Set("byte_size = ?", limit+1).Where("id = ?", fileID).Exec(ctx)
	require.NoError(t, err)
	var conflict *conversationaction.ConflictError
	_, err = send.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID,
	})
	require.ErrorAs(t, err, &conflict, "oversized attachment accepted")
	require.Equal(t, conversationaction.ConflictReasonAttachmentTooLarge, conflict.Reason, "oversized attachment accepted")
	file := &servermodels.File{ID: fileID}
	require.NoError(t, f.db.NewSelect().Model(file).WherePK().Scan(ctx))
	require.Equal(t, string(domain.FileStatusUploaded), file.Status)
}
