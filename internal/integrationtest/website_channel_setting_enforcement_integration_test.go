//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// setWebsiteChannelSetting 直接改写网站渠道的一项访客聊天界面开关。
func setWebsiteChannelSetting(t *testing.T, db *bun.DB, channelID, column string, enabled bool) {
	t.Helper()
	if _, err := db.NewUpdate().Model((*servermodels.WebsiteChannelSetting)(nil)).
		Set("? = ?", bun.Ident(column), enabled).
		Where("channel_id = ?", channelID).
		Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestWebsiteVisitorConversationFollowsMultipleConversationsSetting 验证未开启多会话时未指定会话的消息进入最近有消息的会话，开启后新建会话。
func TestWebsiteVisitorConversationFollowsMultipleConversationsSetting(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	setWebsiteChannelSetting(t, f.db, f.channelID, "multiple_conversations_enabled", false)
	send := func() customerchataction.ReceiveWebsiteCustomerMessageResult {
		t.Helper()
		result, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, ClientMessageID: uuid.NewV7().String(), Body: "再问一个问题",
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := send(); result.CreatedConversation || result.Conversation.ID != f.conversationID {
		t.Fatalf("single conversation result=%+v", result)
	}
	setWebsiteChannelSetting(t, f.db, f.channelID, "multiple_conversations_enabled", true)
	created := send()
	if !created.CreatedConversation || created.Conversation.ID == f.conversationID {
		t.Fatalf("multiple conversations result=%+v", created)
	}
	// 再次关闭多会话后，未指定会话的消息进入最近有消息的会话。
	setWebsiteChannelSetting(t, f.db, f.channelID, "multiple_conversations_enabled", false)
	if result := send(); result.CreatedConversation || result.Conversation.ID != created.Conversation.ID {
		t.Fatalf("recent conversation result=%+v", result)
	}
}

// TestWebsiteVisitorAttachmentsFollowSetting 验证渠道关闭附件后访客不能创建上传，也不能发送已上传的附件。
func TestWebsiteVisitorAttachmentsFollowSetting(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	record := visitorUploadedAttachment(t, f, "问题截图.png", "image/png", 7)
	setWebsiteChannelSetting(t, f.db, f.channelID, "attachments_enabled", false)

	var conflict *conversationaction.ConflictError
	_, err := customerchataction.NewCreateWebsiteVisitorUploadAction(f.db, domain.FileStorageBackendLocal).Execute(ctx, customerchataction.WebsiteVisitorUploadInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, FileName: "第二张.png", ContentType: "image/png", ByteSize: 7,
	})
	if !errors.As(err, &conflict) || conflict.Reason != customerchataction.ConflictReasonAttachmentsDisabled {
		t.Fatalf("upload with attachments disabled: %v", err)
	}
	_, err = f.receive.ExecuteAttachment(ctx, customerchataction.WebsiteCustomerAttachmentMessageInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), FileID: record.ID,
	})
	if !errors.As(err, &conflict) || conflict.Reason != customerchataction.ConflictReasonAttachmentsDisabled {
		t.Fatalf("attachment message with attachments disabled: %v", err)
	}
}

// TestWebsiteVisitorRatingFollowsSetting 验证渠道关闭评价后访客不能提交评价。
func TestWebsiteVisitorRatingFollowsSetting(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	setWebsiteChannelSetting(t, f.db, f.channelID, "rating_enabled", false)
	_, err := customerchataction.NewRateWebsiteServiceSessionAction(f.db, newTestTasks(f.db)).Execute(context.Background(), customerchataction.WebsiteServiceSessionRatingInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, ConversationID: f.conversationID,
		ServiceSessionID: uuid.NewV7().String(), Resolved: true,
	})
	var conflict *conversationaction.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != customerchataction.ConflictReasonServiceSessionNotRateable {
		t.Fatalf("rating with rating disabled: %v", err)
	}
}
