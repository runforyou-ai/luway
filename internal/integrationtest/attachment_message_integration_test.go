//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestAttachmentMessages 验证附件激活、发送幂等、成员资格、首发单聊和历史读取。
func TestAttachmentMessages(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	upload := fileaction.NewCreateUploadAction(f.db)
	send := directchataction.NewSendAttachmentMessageAction(f.db, testEnqueuer, nil)
	query := conversationaction.NewListConversationMessagesQuery(f.db)
	for _, target := range []string{"group", "direct"} {
		file, err := upload.Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
			Purpose: domain.FilePurposeMessageAttachment, FileName: "附件 " + target + ".arbitrary", ContentType: "", ByteSize: domain.FilePartSize + 1,
		})
		require.NoError(t, err)
		require.Equal(t, domain.FilePartSize, file.PartSize)
		require.Equal(t, "application/octet-stream", file.ContentType)
		input := directchataction.AttachmentMessageInput{ConversationID: f.groupID, FileID: file.ID, ClientMessageID: uuid.NewV7().String()}
		_, err = send.Execute(ctx, f.owner, input)
		require.ErrorIs(t, err, fileaction.ErrFileNotFound, "pending upload accepted")
		_, err = markFileUploaded(ctx, f.db, f.owner, file.ID, "")
		require.NoError(t, err)
		_, err = send.Execute(ctx, f.member, input)
		require.ErrorIs(t, err, fileaction.ErrFileNotFound, "another uploader accepted")
		if target == "direct" {
			input.ConversationID = ""
			input.TargetIdentityID = f.member.WorkspaceIdentity.ID
		}
		result, err := send.Execute(ctx, f.owner, input)
		require.NoError(t, err)
		require.NotNil(t, result.Message.ClientMessageID)
		require.Equal(t, input.ClientMessageID, *result.Message.ClientMessageID)
		require.Equal(t, domain.MessageTypeAttachment, result.Message.Type)
		require.Empty(t, result.Message.Body)
		require.NotNil(t, result.Message.Attachment)
		require.Equal(t, file.ID, result.Message.Attachment.ID)
		repeated, err := send.Execute(ctx, f.owner, input)
		require.NoError(t, err)
		require.Equal(t, result.Message.ID, repeated.Message.ID)
		require.NotNil(t, repeated.Message.Attachment)
		require.NotNil(t, repeated.Message.ClientMessageID)
		require.Equal(t, input.ClientMessageID, *repeated.Message.ClientMessageID)
		assertMemberClientAssociation(t, f.db, f.owner, result.ConversationID, result.Message.ID, input.ClientMessageID, f.member)
		changed := input
		changed.FileID = uuid.NewV7().String()
		var conflict *conversationaction.ConflictError
		_, err = send.Execute(ctx, f.owner, changed)
		require.ErrorAs(t, err, &conflict, "changed intent")
		file = &servermodels.File{ID: file.ID}
		require.NoError(t, f.db.NewSelect().Model(file).WherePK().Scan(ctx))
		require.Equal(t, string(domain.FileStatusActive), file.Status, "file not activated")
		require.Nil(t, file.ExpiresAt, "file not activated")
		history, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: result.ConversationID, AroundMessageID: result.Message.ID})
		require.NoError(t, err)
		found := false
		for _, message := range history.Messages {
			if message.ID == result.Message.ID && message.Attachment != nil && message.Attachment.Name == file.OriginalName {
				found = true
			}
		}
		require.True(t, found, "attachment missing from history")
		downloaded, err := query.GetAttachmentFile(ctx, f.member, result.ConversationID, result.Message.ID)
		require.NoError(t, err)
		require.Equal(t, file.ID, downloaded.ID)
		itemsPage, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		items := itemsPage.Conversations
		require.NoError(t, err)
		for _, item := range items {
			if item.ID != result.ConversationID {
				continue
			}
			if target == "direct" {
				require.NotNil(t, item.Direct)
				require.NotNil(t, item.Direct.Preview)
				require.Equal(t, file.OriginalName, *item.Direct.Preview)
			}
			if target == "group" {
				require.NotNil(t, item.Group)
				require.NotNil(t, item.Group.Preview)
				require.Equal(t, file.OriginalName, *item.Group.Preview)
			}
		}
		// 已完成附件在单聊和群聊中均可按文件名引用。
		var reply conversationaction.ConversationMessage
		if target == "group" {
			reply, err = newGroupSendAction(f.db).Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: result.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "收到附件", ReplyToMessageID: result.Message.ID})
		} else {
			reply, err = directchataction.NewSendDirectTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.member, directchataction.InternalTextMessageInput{ConversationID: result.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "收到附件", ReplyToMessageID: result.Message.ID})
		}
		require.NoError(t, err)
		require.NotNil(t, reply.ReplyTo)
		require.Equal(t, file.OriginalName, reply.ReplyTo.Body)
		replyHistory, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: result.ConversationID, AroundMessageID: reply.ID})
		require.NoError(t, err)
		foundReply := false
		for _, message := range replyHistory.Messages {
			if message.ID == reply.ID {
				foundReply = message.ReplyTo != nil && message.ReplyTo.Body == file.OriginalName
			}
		}
		require.True(t, foundReply, "attachment reply missing from history")
		require.NoError(t, filemaintenance.NewCancelUploadAction(f.db).Execute(ctx, f.owner, file.ID))
		active := &servermodels.File{ID: file.ID}
		require.NoError(t, f.db.NewSelect().Model(active).WherePK().Scan(ctx))
		require.Equal(t, string(domain.FileStatusActive), active.Status, "cancel removed sent file")
	}
}

// uploadedAttachment 创建一个已完成上传、尚未发送的消息附件临时文件。
func uploadedAttachment(t *testing.T, db *bun.DB, identity *servermodels.Identity, name, contentType string) string {
	t.Helper()
	ctx := context.Background()
	file, err := fileaction.NewCreateUploadAction(db).Execute(ctx, identity, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeMessageAttachment, FileName: name, ContentType: contentType, ByteSize: 7,
	})
	require.NoError(t, err)
	_, err = markFileUploaded(ctx, db, identity, file.ID, "")
	require.NoError(t, err)
	return file.ID
}

// markFileUploaded 按记录声明的字节数完成成员上传的核验并标记为已上传。
func markFileUploaded(ctx context.Context, db *bun.DB, identity *servermodels.Identity, fileID, etag string) (*servermodels.File, error) {
	return fileaction.NewCompleteUploadAction(db).Execute(ctx, identity, fileID, func(_ context.Context, record *servermodels.File) (serverfilecontent.UploadedObject, error) {
		return uploadedTestObject(record, etag), nil
	})
}

// TestAttachmentMessageSequence 验证附件按发送顺序保存说明和图片尺寸，重放幂等，未完成上传或已过期的文件不能发送。
func TestAttachmentMessageSequence(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	send := directchataction.NewSendAttachmentMessageAction(f.db, testEnqueuer, nil)
	query := conversationaction.NewListConversationMessagesQuery(f.db)
	// 首个附件以对端身份首发单聊，第二个附件带说明发往已建立的会话。
	first := directchataction.AttachmentMessageInput{
		TargetIdentityID: f.member.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(),
		FileID: uploadedAttachment(t, f.db, f.owner, "photo.png", "image/png"), ImageWidth: 320, ImageHeight: 200,
	}
	firstResult, err := send.Execute(ctx, f.owner, first)
	require.NoError(t, err)
	require.NotNil(t, firstResult.Conversation)
	require.NotNil(t, firstResult.Message.Attachment)
	require.Equal(t, 320, firstResult.Message.Attachment.ImageWidth)
	require.Equal(t, 200, firstResult.Message.Attachment.ImageHeight)
	second := directchataction.AttachmentMessageInput{
		ConversationID: firstResult.ConversationID, ClientMessageID: uuid.NewV7().String(),
		FileID: uploadedAttachment(t, f.db, f.owner, "spec.pdf", "application/pdf"), Body: "  文件说明  ",
	}
	secondResult, err := send.Execute(ctx, f.owner, second)
	require.NoError(t, err)
	require.Nil(t, secondResult.Conversation)
	require.Equal(t, "文件说明", secondResult.Message.Body)
	require.Equal(t, firstResult.Message.MessageSeq+1, secondResult.Message.MessageSeq)
	assertMemberClientAssociation(t, f.db, f.owner, secondResult.ConversationID, secondResult.Message.ID, second.ClientMessageID, f.member)
	// 同一发送编号重放返回原消息，改变说明视为冲突。
	repeated, err := send.Execute(ctx, f.owner, second)
	require.NoError(t, err)
	require.Equal(t, secondResult.Message.ID, repeated.Message.ID)
	require.Equal(t, "文件说明", repeated.Message.Body)
	require.NotNil(t, repeated.Message.Attachment)
	changed := second
	changed.Body = "不同说明"
	var conflict *conversationaction.ConflictError
	_, err = send.Execute(ctx, f.owner, changed)
	require.ErrorAs(t, err, &conflict, "changed intent")
	resized := first
	resized.ConversationID, resized.TargetIdentityID, resized.ImageWidth = firstResult.ConversationID, "", 640
	_, err = send.Execute(ctx, f.owner, resized)
	require.ErrorAs(t, err, &conflict, "changed image size")
	// 接收方按发送顺序读取并下载附件，说明成为收件箱摘要。
	history, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: secondResult.ConversationID})
	require.NoError(t, err)
	require.Len(t, history.Messages, 2)
	require.Equal(t, firstResult.Message.ID, history.Messages[0].ID)
	require.Equal(t, "文件说明", history.Messages[1].Body)
	require.NotNil(t, history.Messages[1].Attachment)
	require.Equal(t, "spec.pdf", history.Messages[1].Attachment.Name)
	_, err = query.GetAttachmentFile(ctx, f.member, secondResult.ConversationID, secondResult.Message.ID)
	require.NoError(t, err)
	itemsPage, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	foundConversation := false
	for _, item := range itemsPage.Conversations {
		if item.ID == secondResult.ConversationID {
			foundConversation = item.Direct != nil && item.Direct.Preview != nil && *item.Direct.Preview == "文件说明"
		}
	}
	require.True(t, foundConversation, "caption preview missing from inbox")
	// 已经过期的临时文件不能发送，也不留下消息。
	expired := uploadedAttachment(t, f.db, f.owner, "expired.txt", "text/plain")
	_, err = f.db.NewUpdate().Table("files").Set("expires_at = now() - interval '1 second'").Where("id = ?", expired).Exec(ctx)
	require.NoError(t, err)
	_, err = send.Execute(ctx, f.owner, directchataction.AttachmentMessageInput{ConversationID: secondResult.ConversationID, ClientMessageID: uuid.NewV7().String(), FileID: expired})
	require.ErrorIs(t, err, fileaction.ErrFileNotFound, "expired file accepted")
	count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", secondResult.ConversationID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), count, "messages")
}
