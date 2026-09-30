//go:build server

package integrationtest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	fileaction "github.com/runforyou-ai/cervi/internal/actions/file"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

const websiteVisitorExternalID = "web-session:0123456789abcdef0123456789abcdef"

// visitorUploadedAttachment 以访客身份创建并完成一个附件上传。
func visitorUploadedAttachment(t *testing.T, f customerReadFixture, name, contentType string, byteSize int64) *servermodels.File {
	t.Helper()
	ctx := context.Background()
	create := customerchataction.NewCreateWebsiteVisitorUploadAction(f.db, domain.FileStorageBackendLocal)
	record, err := create.Execute(ctx, customerchataction.WebsiteVisitorUploadInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID,
		FileName: name, ContentType: contentType, ByteSize: byteSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := customerchataction.NewCompleteWebsiteVisitorUploadAction(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID, record.ID,
		func(_ context.Context, file *servermodels.File) (string, int64, error) { return "", file.ByteSize, nil })
	if err != nil {
		t.Fatal(err)
	}
	return completed
}

// TestWebsiteVisitorAttachmentUpload 验证访客上传归属自身渠道身份、不使用分片，并在超过渠道上限时被拒绝。
func TestWebsiteVisitorAttachmentUpload(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	record := visitorUploadedAttachment(t, f, "问题截图.png", "image/png", 7)
	if record.Status != string(domain.FileStatusUploaded) || record.PartSize != 0 || record.UploaderChannelIdentityID == nil {
		t.Fatalf("visitor upload=%+v", record)
	}
	var identityExternalID string
	if err := f.db.NewSelect().Table("contact_channel_identities").Column("external_id").
		Where("id = ?", *record.UploaderChannelIdentityID).Scan(ctx, &identityExternalID); err != nil {
		t.Fatal(err)
	}
	if identityExternalID != websiteVisitorExternalID {
		t.Fatalf("uploader identity=%q", identityExternalID)
	}
	// 完成上传按渠道身份幂等。
	if _, err := customerchataction.NewCompleteWebsiteVisitorUploadAction(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID, record.ID,
		func(context.Context, *servermodels.File) (string, int64, error) {
			return "", 0, errors.New("finalize must not run")
		}); err != nil {
		t.Fatal(err)
	}
	// 其他访客不能完成该上传。
	other := "web-session:fedcba9876543210fedcba9876543210"
	if _, err := customerchataction.NewCompleteWebsiteVisitorUploadAction(f.db).Execute(ctx, f.channelID, other, record.ID,
		func(_ context.Context, file *servermodels.File) (string, int64, error) { return "", file.ByteSize, nil }); err == nil {
		t.Fatal("foreign visitor completed upload")
	}
	create := customerchataction.NewCreateWebsiteVisitorUploadAction(f.db, domain.FileStorageBackendLocal)
	var conflict *conversationaction.ConflictError
	_, err := create.Execute(ctx, customerchataction.WebsiteVisitorUploadInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, FileName: "超大附件.bin",
		ContentType: "application/octet-stream", ByteSize: domain.ChannelInboundAttachmentLimit(domain.ChannelTypeWebsite) + 1,
	})
	if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonAttachmentTooLarge {
		t.Fatalf("oversized upload accepted: %v", err)
	}
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
	if err != nil || result.Message.Attachment == nil || result.Message.Attachment.ID != record.ID ||
		result.Message.Attachment.Name != "问题截图.png" || result.Message.Attachment.ImageWidth != 800 ||
		result.Message.Attachment.TransferStatus != domain.MessageAttachmentTransferReady ||
		result.Message.Attachment.StorageKey == "" || result.OrganizationID != f.owner.Organization.ID {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	file := &servermodels.File{ID: record.ID}
	if err := f.db.NewSelect().Model(file).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if file.Status != string(domain.FileStatusActive) || file.ExpiresAt != nil {
		t.Fatalf("file not activated: %+v", file)
	}
	replay, err := f.receive.ExecuteAttachment(ctx, input)
	if err != nil || replay.Message.ID != result.Message.ID || replay.Message.Attachment == nil || replay.Message.Attachment.ID != record.ID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	// 同一发送编号改变文件或图片尺寸按冲突拒绝。
	var conflict *conversationaction.ConflictError
	changed := input
	changed.ImageWidth = 1024
	if _, err := f.receive.ExecuteAttachment(ctx, changed); !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
		t.Fatalf("changed intent accepted: %v", err)
	}
	// 已激活的文件不能再次关联新消息。
	reuse := input
	reuse.ClientMessageID = uuid.NewV7().String()
	if _, err := f.receive.ExecuteAttachment(ctx, reuse); !errors.Is(err, fileaction.ErrFileNotFound) {
		t.Fatalf("activated file reused: %v", err)
	}
	// 访客历史返回附件元数据与内容位置。
	history, err := customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, ConversationID: f.conversationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	last := history.Messages[len(history.Messages)-1]
	if last.ID != result.Message.ID || last.Attachment == nil || last.Attachment.Name != "问题截图.png" ||
		last.Attachment.StorageBackend != domain.FileStorageBackendLocal || last.Attachment.StorageKey == "" ||
		history.OrganizationID != f.owner.Organization.ID {
		t.Fatalf("history=%+v", last)
	}
	// 会话列表以文件名作为只含附件消息的预览。
	conversationsDirectory, err := customerchataction.NewListWebsiteConversationsQuery(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID)
	conversations := conversationsDirectory.Conversations
	if err != nil || len(conversations) == 0 || conversations[0].ID != f.conversationID || conversations[0].Preview != "问题截图.png" {
		t.Fatalf("conversations=%+v err=%v", conversations, err)
	}
	// 重签查询只对该访客可见的消息返回文件。
	attachmentFile, err := customerchataction.NewGetWebsiteVisitorAttachmentQuery(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID, f.conversationID, result.Message.ID)
	if err != nil || attachmentFile.ID != record.ID {
		t.Fatalf("attachment=%+v err=%v", attachmentFile, err)
	}
	if _, err := customerchataction.NewGetWebsiteVisitorAttachmentQuery(f.db).Execute(ctx, f.channelID, "web-session:fedcba9876543210fedcba9876543210", f.conversationID, result.Message.ID); err == nil {
		t.Fatal("foreign visitor read attachment")
	}
}

// TestWebsiteVisitorAttachmentCreatesConversation 验证访客首条消息为附件时创建会话并以文件名作为标题。
func TestWebsiteVisitorAttachmentCreatesConversation(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	record := visitorUploadedAttachment(t, f, "合同.pdf", "application/pdf", 9)
	result, err := f.receive.ExecuteAttachment(ctx, customerchataction.WebsiteCustomerAttachmentMessageInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID,
		ClientMessageID: uuid.NewV7().String(), FileID: record.ID,
	})
	if err != nil || !result.CreatedConversation || result.Conversation.ID == f.conversationID || result.Conversation.Title != "合同.pdf" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	conversationsDirectory, err := customerchataction.NewListWebsiteConversationsQuery(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID)
	conversations := conversationsDirectory.Conversations
	if err != nil {
		t.Fatal(err)
	}
	for _, conversation := range conversations {
		if conversation.ID == result.Conversation.ID {
			return
		}
	}
	t.Fatalf("attachment conversation missing: %+v", conversations)
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
		if err != nil {
			t.Fatalf("并发第 %d 次提交失败: %v", index+1, err)
		}
	}
	if results[0].Message.ID != results[1].Message.ID {
		t.Fatalf("并发提交产生不同消息: %s %s", results[0].Message.ID, results[1].Message.ID)
	}
	count, err := f.db.NewSelect().Table("message_attachments").Where("file_id = ?", record.ID).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("attachments=%d err=%v", count, err)
	}
}
