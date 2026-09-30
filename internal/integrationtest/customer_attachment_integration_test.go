//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestCustomerAttachmentReply 验证成员向网站客户会话发送附件时激活文件、隐式领取周期并按发送意图幂等。
func TestCustomerAttachmentReply(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	send := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, nil)
	fileID := uploadedAttachment(t, f.db, f.owner, "报价单.pdf", "application/pdf")
	input := servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID, Body: "请查收报价单",
	}
	message, err := send.Execute(ctx, f.owner, input)
	if err != nil || message.Type != domain.MessageTypeAttachment || message.Attachment == nil ||
		message.Attachment.ID != fileID || message.Attachment.Name != "报价单.pdf" ||
		message.Attachment.TransferStatus != domain.MessageAttachmentTransferReady {
		t.Fatalf("message=%+v err=%v", message, err)
	}
	file := &servermodels.File{ID: fileID}
	if err := f.db.NewSelect().Model(file).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if file.Status != string(domain.FileStatusActive) || file.ExpiresAt != nil {
		t.Fatalf("file not activated: %+v", file)
	}
	// 首次回复隐式领取当前客服周期。
	var assignee string
	if err := f.db.NewSelect().Table("service_sessions").ColumnExpr("COALESCE(assignee_identity_id::text, '')").
		Where("conversation_id = ?", f.conversationID).Scan(ctx, &assignee); err != nil {
		t.Fatal(err)
	}
	if assignee != f.owner.OrganizationIdentity.ID {
		t.Fatalf("assignee=%q", assignee)
	}
	replay, err := send.Execute(ctx, f.owner, input)
	if err != nil || replay.ID != message.ID || replay.Attachment == nil || replay.Attachment.ID != fileID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	var conflict *conversationaction.ConflictError
	for _, changed := range []servicesessionaction.ServiceAttachmentMessageInput{
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, FileID: fileID, Body: "改过的说明"},
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, FileID: uuid.NewV7().String(), Body: input.Body},
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, FileID: fileID, Body: input.Body, ImageWidth: 100, ImageHeight: 100},
	} {
		if _, err := send.Execute(ctx, f.owner, changed); !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
			t.Fatalf("changed intent accepted: %v", err)
		}
	}
	// 已发送的附件文件不能再次关联到新消息。
	reuse := servicesessionaction.ServiceAttachmentMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID}
	if _, err := send.Execute(ctx, f.owner, reuse); !errors.Is(err, fileaction.ErrFileNotFound) {
		t.Fatalf("activated file reused: %v", err)
	}
	// 历史窗口返回附件元数据与取回状态。
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil {
		t.Fatal(err)
	}
	last := history.Messages[len(history.Messages)-1]
	if last.ID != message.ID || last.Attachment == nil || last.Attachment.Name != "报价单.pdf" ||
		last.Attachment.TransferStatus != domain.MessageAttachmentTransferReady {
		t.Fatalf("history=%+v", last)
	}
}

// TestCustomerAttachmentChannelLimits 验证渠道附件能力与说明长度在事务内生效。
func TestCustomerAttachmentChannelLimits(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	send := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, nil)
	// 超长说明在输入规范化阶段被拒绝。
	fileID := uploadedAttachment(t, f.db, f.owner, "说明.txt", "text/plain")
	long := make([]rune, 4001)
	for index := range long {
		long[index] = '鹿'
	}
	var conflict *conversationaction.ConflictError
	_, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID, Body: string(long),
	})
	if validation, ok := errors.AsType[*conversationaction.ValidationError](err); !ok || validation.Fields["body"] != conversationaction.ValidationBodyTooLong {
		t.Fatalf("long caption accepted: %v", err)
	}
	// Telegram 渠道按平台的说明上限拒绝发送。
	if _, err := f.db.NewUpdate().Table("channels").Set("type = ?", domain.ChannelTypeTelegram).Where("id = ?", f.channelID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = send.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID, Body: string(long[:domain.ChannelCaptionLimit(domain.ChannelTypeTelegram)+1]),
	})
	if !errors.As(err, &conflict) || conflict.Reason != servicesessionaction.ConflictReasonCaptionTooLong {
		t.Fatalf("long telegram caption accepted: %v", err)
	}
	// 不支持对外回复的渠道拒绝附件。
	if _, err := f.db.NewUpdate().Table("channels").Set("type = ?", domain.ChannelTypeWeChatOfficialAccount).Where("id = ?", f.channelID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = send.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID,
	})
	if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonChannelOutboundUnsupported {
		t.Fatalf("unsupported channel accepted: %v", err)
	}
	// 被拒绝的附件不进入外部投递队列。
	count, err := f.db.NewSelect().Table("customer_message_deliveries").Where("conversation_id = ?", f.conversationID).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("deliveries=%d err=%v", count, err)
	}
}

// TestCustomerAttachmentByteLimit 验证超过渠道字节上限的附件在事务内被拒绝且文件保持未激活。
func TestCustomerAttachmentByteLimit(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	send := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, nil)
	fileID := uploadedAttachment(t, f.db, f.owner, "超大附件.bin", "application/octet-stream")
	limit := domain.ChannelAttachmentLimit(domain.ChannelTypeWebsite)
	if _, err := f.db.NewUpdate().Table("files").Set("byte_size = ?", limit+1).Where("id = ?", fileID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var conflict *conversationaction.ConflictError
	_, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID,
	})
	if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonAttachmentTooLarge {
		t.Fatalf("oversized attachment accepted: %v", err)
	}
	file := &servermodels.File{ID: fileID}
	if err := f.db.NewSelect().Model(file).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if file.Status != string(domain.FileStatusUploaded) {
		t.Fatalf("file status=%q", file.Status)
	}
}
