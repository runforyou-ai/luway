//go:build server

package integrationtest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/domain"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

const websiteVisitorExternalID = "web-session:0123456789abcdef0123456789abcdef"

// visitorUploadedAttachment 以访客身份创建并完成一个附件上传。
func visitorUploadedAttachment(t *testing.T, f customerReadFixture, name, contentType string, byteSize int64) *servermodels.File {
	t.Helper()
	ctx := context.Background()
	create := customerchataction.NewCreateWebsiteVisitorUploadAction(f.db, testEnqueuer, localStorage)
	record, err := create.Execute(ctx, customerchataction.WebsiteVisitorUploadInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID,
		FileName: name, ContentType: contentType, ByteSize: byteSize,
	})
	require.NoError(t, err)
	completed, err := customerchataction.NewCompleteWebsiteVisitorUploadAction(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID, record.ID,
		func(_ context.Context, file *servermodels.File) (serverfilecontent.UploadedObject, error) {
			return uploadedTestObject(file, ""), nil
		})
	require.NoError(t, err)
	return completed
}

// TestWebsiteVisitorAttachmentUpload 验证访客上传归属自身渠道身份、不使用分片，并在超过渠道上限时被拒绝。
func TestWebsiteVisitorAttachmentUpload(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	record := visitorUploadedAttachment(t, f, "问题截图.png", "image/png", 7)
	require.Equal(t, string(domain.FileStatusUploaded), record.Status)
	require.Equal(t, int64(0), record.PartSize)
	require.NotNil(t, record.UploaderChannelIdentityID)
	var identityExternalID string
	require.NoError(t, f.db.NewSelect().Table("channel_identities").Column("external_id").
		Where("id = ?", *record.UploaderChannelIdentityID).Scan(ctx, &identityExternalID))
	require.Equal(t, websiteVisitorExternalID, identityExternalID, "uploader identity")
	// 完成上传按渠道身份幂等。
	_, err := customerchataction.NewCompleteWebsiteVisitorUploadAction(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID, record.ID,
		func(context.Context, *servermodels.File) (serverfilecontent.UploadedObject, error) {
			return serverfilecontent.UploadedObject{}, errors.New("finalize must not run")
		})
	require.NoError(t, err)
	// 其他访客不能完成该上传。
	other := "web-session:fedcba9876543210fedcba9876543210"
	_, err = customerchataction.NewCompleteWebsiteVisitorUploadAction(f.db).Execute(ctx, f.channelID, other, record.ID,
		func(_ context.Context, file *servermodels.File) (serverfilecontent.UploadedObject, error) {
			return uploadedTestObject(file, ""), nil
		})
	require.Error(t, err, "foreign visitor completed upload")
	create := customerchataction.NewCreateWebsiteVisitorUploadAction(f.db, testEnqueuer, localStorage)
	var conflict *conversationaction.ConflictError
	_, err = create.Execute(ctx, customerchataction.WebsiteVisitorUploadInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, FileName: "超大附件.bin",
		ContentType: "application/octet-stream", ByteSize: domain.ChannelCapabilitiesOf(domain.ChannelTypeWebsite).InboundAttachmentLimit + 1,
	})
	require.ErrorAs(t, err, &conflict, "oversized upload accepted")
	require.Equal(t, conversationaction.ConflictReasonAttachmentTooLarge, conflict.Reason, "oversized upload accepted")
}

// TestWebsiteVisitorAttachmentMessage 验证访客附件消息激活文件、按发送意图幂等，并出现在访客历史与会话列表中。
func TestWebsiteVisitorAttachmentMessage(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	record := visitorUploadedAttachment(t, f, "问题截图.png", "image/png", 7)
	input := customerchataction.WebsiteCustomerAttachmentMessageInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), FileID: record.ID, ImageWidth: 800, ImageHeight: 600,
	}
	result, err := f.receive.ExecuteAttachment(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result.Message.Attachment)
	require.Equal(t, record.ID, result.Message.Attachment.ID)
	require.Equal(t, "问题截图.png", result.Message.Attachment.Name)
	require.Equal(t, 800, result.Message.Attachment.ImageWidth)
	require.Equal(t, domain.MessageAttachmentTransferReady, result.Message.Attachment.TransferStatus)
	require.NotEmpty(t, result.Message.Attachment.StorageKey)
	require.Equal(t, f.owner.Workspace.ID, result.WorkspaceID)
	file := &servermodels.File{ID: record.ID}
	require.NoError(t, f.db.NewSelect().Model(file).WherePK().Scan(ctx))
	require.Equal(t, string(domain.FileStatusActive), file.Status, "file not activated")
	require.Nil(t, file.ExpiresAt, "file not activated")
	replay, err := f.receive.ExecuteAttachment(ctx, input)
	require.NoError(t, err)
	require.Equal(t, result.Message.ID, replay.Message.ID)
	require.NotNil(t, replay.Message.Attachment)
	require.Equal(t, record.ID, replay.Message.Attachment.ID)
	// 同一发送编号改变文件或图片尺寸按冲突拒绝。
	var conflict *conversationaction.ConflictError
	changed := input
	changed.ImageWidth = 1024
	_, err = f.receive.ExecuteAttachment(ctx, changed)
	require.ErrorAs(t, err, &conflict, "changed intent accepted")
	require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason, "changed intent accepted")
	// 已激活的文件不能再次关联新消息。
	reuse := input
	reuse.ClientMessageID = uuid.NewV7().String()
	_, err = f.receive.ExecuteAttachment(ctx, reuse)
	require.ErrorIs(t, err, fileaction.ErrFileNotFound, "activated file reused")
	// 访客历史返回附件元数据与内容位置。
	history, err := customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, ConversationID: f.conversationID,
	})
	require.NoError(t, err)
	last := history.Messages[len(history.Messages)-1]
	require.Equal(t, result.Message.ID, last.ID)
	require.NotNil(t, last.Attachment)
	require.Equal(t, "问题截图.png", last.Attachment.Name)
	require.Equal(t, domain.FileStorageBackendLocal, last.Attachment.StorageBackend)
	require.NotEmpty(t, last.Attachment.StorageKey)
	require.Equal(t, f.owner.Workspace.ID, history.WorkspaceID)
	// 会话列表以文件名作为只含附件消息的预览。
	conversationsDirectory, err := customerchataction.NewListWebsiteConversationsQuery(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID)
	conversations := conversationsDirectory.Conversations
	require.NoError(t, err)
	require.NotEmpty(t, conversations)
	require.Equal(t, f.conversationID, conversations[0].ID)
	require.Equal(t, "问题截图.png", conversations[0].Preview)
	// 重签查询只对该访客可见的消息返回文件。
	attachmentFile, err := customerchataction.NewGetWebsiteVisitorAttachmentQuery(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID, f.conversationID, result.Message.ID)
	require.NoError(t, err)
	require.Equal(t, record.ID, attachmentFile.ID)
	_, err = customerchataction.NewGetWebsiteVisitorAttachmentQuery(f.db).Execute(ctx, f.channelID, "web-session:fedcba9876543210fedcba9876543210", f.conversationID, result.Message.ID)
	require.Error(t, err, "foreign visitor read attachment")
}

// TestWebsiteVisitorAttachmentConcurrentReplay 验证同一发送编号并发提交时，后提交的一次按幂等记录返回同一条消息。
func TestWebsiteVisitorAttachmentConcurrentReplay(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	record := visitorUploadedAttachment(t, f, "并发截图.png", "image/png", 7)
	input := customerchataction.WebsiteCustomerAttachmentMessageInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), FileID: record.ID,
	}
	results := make([]customerchataction.ReceiveWebsiteCustomerMessageResult, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range results {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			results[index], errs[index] = f.receive.ExecuteAttachment(ctx, input)
		}(index)
	}
	close(start)
	group.Wait()
	for index, err := range errs {
		require.NoError(t, err, "并发第 %d 次提交失败", index+1)
	}
	require.Equal(t, results[0].Message.ID, results[1].Message.ID, "并发提交产生不同消息")
	count, err := f.db.NewSelect().Table("message_attachments").Where("file_id = ?", record.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "attachments")
}
