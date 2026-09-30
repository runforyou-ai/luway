//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	fileaction "github.com/runforyou-ai/cervi/internal/actions/file"
	"github.com/runforyou-ai/cervi/internal/actions/filemaintenance"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestAttachmentMessageValidation 验证附件消息的参数错误按字段返回校验错误。
func TestAttachmentMessageValidation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	send := directchataction.NewSendAttachmentMessageAction(f.db, nil)
	for _, scenario := range []struct {
		name  string
		input directchataction.AttachmentMessageInput
		field string
		code  conversationaction.ValidationCode
	}{
		{"正文超长", directchataction.AttachmentMessageInput{ConversationID: f.groupID, FileID: uuid.NewV7().String(), ClientMessageID: uuid.NewV7().String(), Body: strings.Repeat("字", conversationaction.MaxMessageBodyRunes+1)}, "body", conversationaction.ValidationBodyTooLong},
		{"文件编号无效", directchataction.AttachmentMessageInput{ConversationID: f.groupID, FileID: "file", ClientMessageID: uuid.NewV7().String()}, "fileId", conversationaction.ValidationFileIDInvalid},
		{"客户端消息编号无效", directchataction.AttachmentMessageInput{ConversationID: f.groupID, FileID: uuid.NewV7().String(), ClientMessageID: "message"}, "clientMessageId", conversationaction.ValidationClientMessageIDInvalid},
		{"会话与目标同时缺失", directchataction.AttachmentMessageInput{FileID: uuid.NewV7().String(), ClientMessageID: uuid.NewV7().String()}, "conversationId", conversationaction.ValidationConversationIDInvalid},
		{"会话与目标同时给出", directchataction.AttachmentMessageInput{ConversationID: f.groupID, TargetIdentityID: f.member.OrganizationIdentity.ID, FileID: uuid.NewV7().String(), ClientMessageID: uuid.NewV7().String()}, "conversationId", conversationaction.ValidationConversationIDInvalid},
		{"单聊目标编号无效", directchataction.AttachmentMessageInput{TargetIdentityID: "member", FileID: uuid.NewV7().String(), ClientMessageID: uuid.NewV7().String()}, "targetIdentityId", conversationaction.ValidationTargetIdentityIDInvalid},
		{"AI 员工编号无效", directchataction.AttachmentMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: "agent", FileID: uuid.NewV7().String(), ClientMessageID: uuid.NewV7().String()}, "agentIdentityId", conversationaction.ValidationTargetIdentityIDInvalid},
		{"所服务的客户会话编号无效", directchataction.AttachmentMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: uuid.NewV7().String(), ServedConversationID: "served", FileID: uuid.NewV7().String(), ClientMessageID: uuid.NewV7().String()}, "servedConversationId", conversationaction.ValidationConversationIDInvalid},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, err := send.Execute(context.Background(), f.owner, scenario.input)
			validation, ok := errors.AsType[*conversationaction.ValidationError](err)
			if !ok || validation.Fields[scenario.field] != scenario.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

// TestAttachmentMessages 验证附件激活、发送幂等、成员资格、首发单聊和历史读取。
func TestAttachmentMessages(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	upload := fileaction.NewCreateUploadAction(f.db)
	send := directchataction.NewSendAttachmentMessageAction(f.db, nil)
	query := conversationaction.NewListConversationMessagesQuery(f.db)
	for _, target := range []string{"group", "direct"} {
		file, err := upload.Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
			Purpose: domain.FilePurposeMessageAttachment, FileName: "附件 " + target + ".arbitrary", ContentType: "", ByteSize: domain.FilePartSize + 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if file.PartSize != domain.FilePartSize || file.ContentType != "application/octet-stream" {
			t.Fatalf("file=%+v", file)
		}
		input := directchataction.AttachmentMessageInput{ConversationID: f.groupID, FileID: file.ID, ClientMessageID: uuid.NewV7().String()}
		if _, err := send.Execute(ctx, f.owner, input); !errors.Is(err, fileaction.ErrFileNotFound) {
			t.Fatalf("pending upload accepted: %v", err)
		}
		if _, err := markFileUploaded(ctx, f.db, f.owner, file.ID, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := send.Execute(ctx, f.member, input); !errors.Is(err, fileaction.ErrFileNotFound) {
			t.Fatalf("another uploader accepted: %v", err)
		}
		if target == "direct" {
			input.ConversationID = ""
			input.TargetIdentityID = f.member.OrganizationIdentity.ID
		}
		result, err := send.Execute(ctx, f.owner, input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Message.ClientMessageID == nil || *result.Message.ClientMessageID != input.ClientMessageID || result.Message.Type != domain.MessageTypeAttachment || result.Message.Body != "" || result.Message.Attachment == nil || result.Message.Attachment.ID != file.ID {
			t.Fatalf("result=%+v", result)
		}
		repeated, err := send.Execute(ctx, f.owner, input)
		if err != nil || repeated.Message.ID != result.Message.ID || repeated.Message.Attachment == nil || repeated.Message.ClientMessageID == nil || *repeated.Message.ClientMessageID != input.ClientMessageID {
			t.Fatalf("repeat=%+v err=%v", repeated, err)
		}
		assertMemberClientAssociation(t, f.db, f.owner, result.ConversationID, result.Message.ID, input.ClientMessageID, f.member)
		changed := input
		changed.FileID = uuid.NewV7().String()
		var conflict *conversationaction.ConflictError
		if _, err := send.Execute(ctx, f.owner, changed); !errors.As(err, &conflict) {
			t.Fatalf("changed intent=%v", err)
		}
		file = &servermodels.File{ID: file.ID}
		if err := f.db.NewSelect().Model(file).WherePK().Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if file.Status != string(domain.FileStatusActive) || file.ExpiresAt != nil {
			t.Fatalf("file not activated: %+v", file)
		}
		history, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: result.ConversationID, AroundMessageID: result.Message.ID})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range history.Messages {
			if message.ID == result.Message.ID && message.Attachment != nil && message.Attachment.Name == file.OriginalName {
				found = true
			}
		}
		if !found {
			t.Fatal("attachment missing from history")
		}
		downloaded, err := query.GetAttachmentFile(ctx, f.member, result.ConversationID, result.Message.ID)
		if err != nil || downloaded.ID != file.ID {
			t.Fatalf("download=%+v err=%v", downloaded, err)
		}
		itemsPage, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		items := itemsPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID != result.ConversationID {
				continue
			}
			if target == "direct" && (item.Direct == nil || item.Direct.Preview == nil || *item.Direct.Preview != file.OriginalName) {
				t.Fatalf("direct preview=%+v", item.Direct)
			}
			if target == "group" && (item.Group == nil || item.Group.Preview == nil || *item.Group.Preview != file.OriginalName) {
				t.Fatalf("group preview=%+v", item.Group)
			}
		}
		// 已完成附件在单聊和群聊中均可按文件名引用。
		var reply conversationaction.ConversationMessage
		if target == "group" {
			reply, err = newGroupSendAction(f.db).Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: result.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "收到附件", ReplyToMessageID: result.Message.ID})
		} else {
			reply, err = directchataction.NewSendDirectTextMessageAction(f.db).Execute(ctx, f.member, directchataction.InternalTextMessageInput{ConversationID: result.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "收到附件", ReplyToMessageID: result.Message.ID})
		}
		if err != nil || reply.ReplyTo == nil || reply.ReplyTo.Body != file.OriginalName {
			t.Fatalf("attachment reply=%+v err=%v", reply, err)
		}
		replyHistory, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: result.ConversationID, AroundMessageID: reply.ID})
		if err != nil {
			t.Fatal(err)
		}
		foundReply := false
		for _, message := range replyHistory.Messages {
			if message.ID == reply.ID {
				foundReply = message.ReplyTo != nil && message.ReplyTo.Body == file.OriginalName
			}
		}
		if !foundReply {
			t.Fatal("attachment reply missing from history")
		}
		if err := filemaintenance.NewCancelUploadAction(f.db).Execute(ctx, f.owner, file.ID); err != nil {
			t.Fatal(err)
		}
		active := &servermodels.File{ID: file.ID}
		if err := f.db.NewSelect().Model(active).WherePK().Scan(ctx); err != nil || active.Status != string(domain.FileStatusActive) {
			t.Fatalf("cancel removed sent file: %+v %v", active, err)
		}
	}
}

// uploadedAttachment 创建一个已完成上传、尚未发送的消息附件临时文件。
func uploadedAttachment(t *testing.T, db *bun.DB, identity *servermodels.Identity, name, contentType string) string {
	t.Helper()
	ctx := context.Background()
	file, err := fileaction.NewCreateUploadAction(db).Execute(ctx, identity, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeMessageAttachment, FileName: name, ContentType: contentType, ByteSize: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := markFileUploaded(ctx, db, identity, file.ID, ""); err != nil {
		t.Fatal(err)
	}
	return file.ID
}

// markFileUploaded 按记录声明的字节数完成成员上传的核验并标记为已上传。
func markFileUploaded(ctx context.Context, db *bun.DB, identity *servermodels.Identity, fileID, etag string) (*servermodels.File, error) {
	return fileaction.NewCompleteUploadAction(db).Execute(ctx, identity, fileID, func(_ context.Context, record *servermodels.File) (string, int64, error) {
		return etag, record.ByteSize, nil
	})
}

// TestAttachmentMessageSequence 验证附件按发送顺序保存说明和图片尺寸，重放幂等，未完成上传或已过期的文件不能发送。
func TestAttachmentMessageSequence(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	send := directchataction.NewSendAttachmentMessageAction(f.db, nil)
	query := conversationaction.NewListConversationMessagesQuery(f.db)
	// 首个附件以对端身份首发单聊，第二个附件带说明发往已建立的会话。
	first := directchataction.AttachmentMessageInput{
		TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(),
		FileID: uploadedAttachment(t, f.db, f.owner, "photo.png", "image/png"), ImageWidth: 320, ImageHeight: 200,
	}
	firstResult, err := send.Execute(ctx, f.owner, first)
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.Conversation == nil || firstResult.Message.Attachment == nil || firstResult.Message.Attachment.ImageWidth != 320 || firstResult.Message.Attachment.ImageHeight != 200 {
		t.Fatalf("first=%+v", firstResult)
	}
	second := directchataction.AttachmentMessageInput{
		ConversationID: firstResult.ConversationID, ClientMessageID: uuid.NewV7().String(),
		FileID: uploadedAttachment(t, f.db, f.owner, "spec.pdf", "application/pdf"), Body: "  文件说明  ",
	}
	secondResult, err := send.Execute(ctx, f.owner, second)
	if err != nil || secondResult.Conversation != nil || secondResult.Message.Body != "文件说明" || secondResult.Message.MessageSeq != firstResult.Message.MessageSeq+1 {
		t.Fatalf("second=%+v err=%v", secondResult, err)
	}
	assertMemberClientAssociation(t, f.db, f.owner, secondResult.ConversationID, secondResult.Message.ID, second.ClientMessageID, f.member)
	// 同一发送编号重放返回原消息，改变说明视为冲突。
	repeated, err := send.Execute(ctx, f.owner, second)
	if err != nil || repeated.Message.ID != secondResult.Message.ID || repeated.Message.Body != "文件说明" || repeated.Message.Attachment == nil {
		t.Fatalf("repeat=%+v err=%v", repeated, err)
	}
	changed := second
	changed.Body = "不同说明"
	var conflict *conversationaction.ConflictError
	if _, err := send.Execute(ctx, f.owner, changed); !errors.As(err, &conflict) {
		t.Fatalf("changed intent=%v", err)
	}
	resized := first
	resized.ConversationID, resized.TargetIdentityID, resized.ImageWidth = firstResult.ConversationID, "", 640
	if _, err := send.Execute(ctx, f.owner, resized); !errors.As(err, &conflict) {
		t.Fatalf("changed image size=%v", err)
	}
	// 接收方按发送顺序读取并下载附件，说明成为收件箱摘要。
	history, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: secondResult.ConversationID})
	if err != nil || len(history.Messages) != 2 || history.Messages[0].ID != firstResult.Message.ID || history.Messages[1].Body != "文件说明" || history.Messages[1].Attachment == nil || history.Messages[1].Attachment.Name != "spec.pdf" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if _, err := query.GetAttachmentFile(ctx, f.member, secondResult.ConversationID, secondResult.Message.ID); err != nil {
		t.Fatal(err)
	}
	itemsPage, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil {
		t.Fatal(err)
	}
	foundConversation := false
	for _, item := range itemsPage.Conversations {
		if item.ID == secondResult.ConversationID {
			foundConversation = item.Direct != nil && item.Direct.Preview != nil && *item.Direct.Preview == "文件说明"
		}
	}
	if !foundConversation {
		t.Fatal("caption preview missing from inbox")
	}
	// 已经过期的临时文件不能发送，也不留下消息。
	expired := uploadedAttachment(t, f.db, f.owner, "expired.txt", "text/plain")
	if _, err := f.db.NewUpdate().Table("files").Set("expires_at = now() - interval '1 second'").Where("id = ?", expired).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := send.Execute(ctx, f.owner, directchataction.AttachmentMessageInput{ConversationID: secondResult.ConversationID, ClientMessageID: uuid.NewV7().String(), FileID: expired}); !errors.Is(err, fileaction.ErrFileNotFound) {
		t.Fatalf("expired file accepted: %v", err)
	}
	count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", secondResult.ConversationID).Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("messages=%d err=%v", count, err)
	}
}

// TestAttachmentMessageReplies 验证附件引用的摘要、幂等和会话隔离。
func TestAttachmentMessageReplies(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "附件的说明"} {
		t.Run("body="+body, func(t *testing.T) {
			f := newNavigationFixture(t)
			ctx := context.Background()
			sent, err := directchataction.NewSendAttachmentMessageAction(f.db, nil).Execute(ctx, f.owner, directchataction.AttachmentMessageInput{
				TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: body,
				FileID: uploadedAttachment(t, f.db, f.owner, "report.txt", "text/plain"),
			})
			if err != nil {
				t.Fatal(err)
			}
			target := sent.Message
			expected := body
			if expected == "" {
				expected = "report.txt"
			}
			send := directchataction.NewSendDirectTextMessageAction(f.db)
			input := directchataction.InternalTextMessageInput{ConversationID: sent.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "收到", ReplyToMessageID: target.ID}
			reply, err := send.Execute(ctx, f.member, input)
			if err != nil || reply.ReplyTo == nil || reply.ReplyTo.ID != target.ID || reply.ReplyTo.Type != domain.MessageTypeAttachment || reply.ReplyTo.Body != expected || reply.ReplyTo.Sender.SourceID != f.owner.OrganizationIdentity.ID {
				t.Fatalf("reply=%+v err=%v", reply, err)
			}
			replayed, err := send.Execute(ctx, f.member, input)
			if err != nil || replayed.ID != reply.ID || replayed.ReplyTo == nil || replayed.ReplyTo.Body != expected {
				t.Fatalf("replayed=%+v err=%v", replayed, err)
			}
			// 核验群聊附件引用的会话归属。
			_, err = newGroupSendAction(f.db).Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "跨会话引用", ReplyToMessageID: target.ID})
			var conflict *conversationaction.ConflictError
			if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonReplyTargetInvalid {
				t.Fatalf("cross-conversation reference=%v", err)
			}
			history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: sent.ConversationID})
			if err != nil || len(history.Messages) != 2 || history.Messages[1].ReplyTo == nil || history.Messages[1].ReplyTo.Body != expected || history.Messages[1].ReplyTo.Type != domain.MessageTypeAttachment {
				t.Fatalf("history=%+v err=%v", history, err)
			}
		})
	}
}
