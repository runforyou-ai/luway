//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// TestConversationAvatarsFollowIdentity 验证单聊、群消息及引用共用身份头像并读取资料更新。
func TestConversationAvatarsFollowIdentity(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	avatarID := uuid.NewV7().String()
	if _, err := f.db.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).Set("avatar_file_id = ?", avatarID).Where("id = ?", f.owner.OrganizationIdentity.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	f.owner.OrganizationIdentity.AvatarFileID = &avatarID
	first := f.send(t, f.owner, "有头像的消息", false)
	assertMessageAvatar(t, first.Sender, avatarID)
	reply, err := newGroupSendAction(f.db).Execute(ctx, f.member, groupchataction.GroupTextMessageInput{
		ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "引用消息", ReplyToMessageID: first.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMessageAvatar(t, reply.ReplyTo.Sender, avatarID)

	start := directchataction.NewSendFirstDirectTextMessageAction(f.db)
	direct, err := start.Execute(ctx, f.member, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: f.owner.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊消息",
	})
	if err != nil || direct.Conversation.PeerAvatarFileID == nil || *direct.Conversation.PeerAvatarFileID != avatarID {
		t.Fatalf("direct=%+v err=%v", direct, err)
	}

	sendDirect := directchataction.NewSendDirectTextMessageAction(f.db)
	directMessage, err := sendDirect.Execute(ctx, f.owner, directchataction.InternalTextMessageInput{
		ConversationID: direct.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊回复目标",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMessageAvatar(t, directMessage.Sender, avatarID)

	// 修改身份资料后，历史消息、单聊查询和列表都应读取新头像。
	updatedAvatarID := uuid.NewV7().String()
	if _, err := f.db.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).Set("avatar_file_id = ?", updatedAvatarID).Where("id = ?", f.owner.OrganizationIdentity.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 首次发送与幂等重试都应补齐单聊引用发送者的最新头像。
	directReplyInput := directchataction.InternalTextMessageInput{
		ConversationID: direct.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊引用", ReplyToMessageID: directMessage.ID,
	}
	for range 2 {
		directReply, err := sendDirect.Execute(ctx, f.member, directReplyInput)
		if err != nil {
			t.Fatal(err)
		}
		if directReply.ReplyTo == nil {
			t.Fatal("单聊引用缺失")
		}
		assertMessageAvatar(t, directReply.ReplyTo.Sender, updatedAvatarID)
	}

	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
	if err != nil {
		t.Fatal(err)
	}
	foundMessage, foundReply := false, false
	for _, message := range history.Messages {
		if message.ID == first.ID {
			assertMessageAvatar(t, message.Sender, updatedAvatarID)
			foundMessage = true
		}
		if message.ID == reply.ID {
			assertMessageAvatar(t, message.ReplyTo.Sender, updatedAvatarID)
			foundReply = true
		}
	}
	if !foundMessage || !foundReply {
		t.Fatal("消息或引用未出现在历史窗口")
	}
	lookup, err := directchataction.NewFindDirectConversationQuery(f.db).Execute(ctx, f.member, f.owner.OrganizationIdentity.ID)
	if err != nil || lookup == nil || lookup.PeerAvatarFileID == nil || *lookup.PeerAvatarFileID != updatedAvatarID {
		t.Fatalf("lookup=%+v err=%v", lookup, err)
	}
	itemsPage, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	items := itemsPage.Conversations
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == direct.Conversation.ID {
			if item.Direct == nil || item.Direct.PeerAvatarFileID == nil || *item.Direct.PeerAvatarFileID != updatedAvatarID {
				t.Fatalf("inbox direct=%+v", item.Direct)
			}
			return
		}
	}
	t.Fatal("收件箱缺少单聊")
}

// assertMessageAvatar 检查消息发送者保留成员头像和身份类型。
func assertMessageAvatar(t *testing.T, sender *conversationaction.ConversationMessageSender, avatarID string) {
	t.Helper()
	if sender == nil || sender.AvatarFileID == nil || *sender.AvatarFileID != avatarID || sender.IdentityType == nil || *sender.IdentityType != domain.OrganizationIdentityTypeUser {
		t.Fatalf("sender=%+v, want avatar=%s and user identity", sender, avatarID)
	}
}
