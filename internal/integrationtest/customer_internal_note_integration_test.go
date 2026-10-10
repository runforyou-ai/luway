//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestCustomerInternalNotes 验证内部备注的写入资格、周期摘要、客户侧隔离和引用边界。
func TestCustomerInternalNotes(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	visitorMessage, err := f.visitorMessage(ctx, "我的订单还没发货")
	require.NoError(t, err)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	noteInput := servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
		Body: "客户上个月投诉过物流", Visibility: domain.MessageVisibilityInternal,
	}
	// 未领取周期的其他客服同样可以留内部备注。
	note, err := send.Execute(ctx, f.member, noteInput)
	require.NoError(t, err)
	require.Equal(t, domain.MessageVisibilityInternal, note.Visibility)

	session := &servermodels.ServiceSession{}
	require.NoError(t, f.db.NewSelect().Model(session).Where("ss.conversation_id = ?", f.conversationID).Scan(ctx))
	require.Nil(t, session.AssigneeIdentityID)
	require.Nil(t, session.FirstResponseAt)
	// 周期摘要仍指向客户末条消息，转交给 AI 时据此补触发。
	require.Equal(t, visitorMessage.Message.ID, session.LastMessageID)
	conversation := &servermodels.Conversation{}
	require.NoError(t, f.db.NewSelect().Model(conversation).Where("cv.id = ?", f.conversationID).Scan(ctx))
	// 内部备注不推进会话摘要，会话末条仍是客户消息。
	require.NotNil(t, conversation.LastMessageID)
	require.Equal(t, visitorMessage.Message.ID, *conversation.LastMessageID)
	// 成员收件箱摘要取到内部备注时标明可见范围。
	inboxPage, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterUnassigned})
	require.NoError(t, err)
	listed := false
	for _, row := range inboxPage.Conversations {
		if row.ID != f.conversationID {
			continue
		}
		listed = true
		require.NotNil(t, row.Service)
		require.NotNil(t, row.Service.Preview)
		require.Equal(t, noteInput.Body, *row.Service.Preview)
		require.NotNil(t, row.Service.PreviewVisibility)
		require.Equal(t, domain.MessageVisibilityInternal, *row.Service.PreviewVisibility)
	}
	require.True(t, listed, "customer conversation missing from inbox")

	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	last := history.Messages[len(history.Messages)-1]
	require.Equal(t, note.ID, last.ID)
	require.Equal(t, domain.MessageVisibilityInternal, last.Visibility)

	visitor := direct.NewWebsiteVisitorBackend(f.db, nil, testEnqueuer, nil, servertest.TestDeployment(t, f.db).S3, servertest.DisabledMail{}, nil)
	externalID := "web-session:0123456789abcdef0123456789abcdef"
	page, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, externalID, f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	require.NoError(t, err)
	for _, message := range page.Messages {
		require.NotEqual(t, note.ID, message.ID, "visitor history contains internal note")
		require.NotEqual(t, noteInput.Body, message.Body, "visitor history contains internal note")
	}
	threads, err := visitor.ListConversations(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, externalID)
	require.NoError(t, err)
	for _, thread := range threads.Conversations {
		require.NotEqual(t, noteInput.Body, thread.Preview, "visitor directory preview contains internal note")
	}

	// 访客发送回包中的会话摘要同样只取对客消息。
	replay, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: externalID, ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), Body: "还有别的办法吗",
	})
	require.NoError(t, err)
	require.NotEqual(t, noteInput.Body, replay.Conversation.Preview, "visitor send summary contains internal note")

	t.Run("对客消息不能引用内部备注", func(t *testing.T) {
		_, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
			Body: "已为您加急", ReplyToMessageID: note.ID,
		})
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, conversationaction.ConflictReasonReplyTargetInvalid, conflict.Reason)
	})

	t.Run("内部备注可以引用备注和对客消息", func(t *testing.T) {
		for _, target := range []string{note.ID, visitorMessage.Message.ID} {
			saved, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
				ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
				Body: "我来跟进", ReplyToMessageID: target, Visibility: domain.MessageVisibilityInternal,
			})
			require.NoError(t, err, "note reply to %s", target)
			require.NotNil(t, saved.ReplyTo, "note reply to %s", target)
			require.Equal(t, target, saved.ReplyTo.ID)
		}
	})

	t.Run("对客引用资格排除内部备注", func(t *testing.T) {
		login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.member.Account.Email, "password123")
		backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
		window, err := backend.ListConversationMessages(ctx, appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID}, f.conversationID, appservice.ConversationMessageListInput{})
		require.NoError(t, err)
		checked := false
		for _, message := range window.Messages {
			if message.ID != note.ID {
				continue
			}
			checked = true
			require.False(t, message.CanReply)
			require.True(t, message.CanNoteReply)
		}
		require.True(t, checked, "internal note missing from member timeline")
	})

	t.Run("客户不能引用内部备注", func(t *testing.T) {
		_, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: externalID, ConversationID: &f.conversationID,
			ClientMessageID: uuid.NewV7().String(), Body: "这是什么", ReplyToMessageID: note.ID,
		})
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, conversationaction.ConflictReasonReplyTargetInvalid, conflict.Reason)
	})

	t.Run("同一消息编号跨可见范围冲突", func(t *testing.T) {
		changed := noteInput
		changed.Visibility = domain.MessageVisibilityShared
		_, err := send.Execute(ctx, f.member, changed)
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason)
		replay, err := send.Execute(ctx, f.member, noteInput)
		require.NoError(t, err)
		require.Equal(t, note.ID, replay.ID)
	})

	t.Run("关闭周期后仍可补记内部备注", func(t *testing.T) {
		tasks := servertest.NewTasks()
		closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, tasks, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer)
		_, err := closeSession.Execute(ctx, f.owner, f.conversationID)
		require.NoError(t, err)
		saved, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
			Body: "补一条备注", Visibility: domain.MessageVisibilityInternal,
		})
		require.NoError(t, err)
		require.Equal(t, domain.MessageVisibilityInternal, saved.Visibility)
		// 补记不重开周期，也不推进周期摘要。
		closed := &servermodels.ServiceSession{}
		require.NoError(t, f.db.NewSelect().Model(closed).Where("ss.conversation_id = ?", f.conversationID).Scan(ctx))
		require.Equal(t, string(domain.ServiceSessionStatusClosed), closed.Status)
		require.NotEqual(t, saved.ID, closed.LastMessageID)
	})
}
