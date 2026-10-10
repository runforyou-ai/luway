//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// assertMemberClientAssociation 核对持久关联和按请求身份隔离的消息窗口。
func assertMemberClientAssociation(t *testing.T, db *bun.DB, sender *servermodels.Identity, conversationID, messageID, clientID string, readers ...*servermodels.Identity) {
	t.Helper()
	ctx := context.Background()
	stored := &servermodels.Message{ID: messageID}
	require.NoError(t, db.NewSelect().Model(stored).WherePK().Scan(ctx))
	require.Equal(t, &clientID, stored.ClientMessageID, "missing independent sender association")
	require.NotNil(t, stored.SenderParticipantID, "missing independent sender association")
	require.NotNil(t, stored.IdempotencyKey, "missing independent sender association")
	require.NotEqual(t, clientID, *stored.IdempotencyKey, "missing independent sender association")
	for _, reader := range append([]*servermodels.Identity{sender}, readers...) {
		page, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, reader, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, AroundMessageID: messageID})
		require.NoError(t, err)
		found := false
		for _, message := range page.Messages {
			if message.ID != messageID {
				continue
			}
			found = true
			if reader.WorkspaceIdentity.ID == sender.WorkspaceIdentity.ID {
				require.Equal(t, &clientID, message.ClientMessageID, "sender association")
			} else {
				require.Nil(t, message.ClientMessageID, "association leaked to another member")
			}
		}
		require.True(t, found, "message %s missing", messageID)
	}
}

// TestMemberClientMessageAssociation 验证成员并发重放、发送方作用域及完整发送意图冲突。
func TestMemberClientMessageAssociation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	send := newGroupSendAction(f.db)
	original := f.send(t, f.member, "被引用的消息", false)
	clientID := uuid.NewV7().String()
	input := groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: strings.ToUpper(clientID), Body: "相同正文", ReplyToMessageID: original.ID, MentionSubjectIDs: []string{f.subjectID}}
	results := make([]conversationaction.ConversationMessage, 4)
	failures := make([]error, len(results))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Go(func() {
			<-start
			results[i], failures[i] = send.Execute(ctx, f.owner, input)
		})
	}
	close(start)
	wg.Wait()
	for i, result := range results {
		require.NoError(t, failures[i], "replay %d", i)
		require.Equal(t, results[0].ID, result.ID, "replay %d", i)
		require.Equal(t, &clientID, result.ClientMessageID, "replay %d", i)
	}
	count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ? AND client_message_id = ?", f.groupID, clientID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "replay count")
	assertMemberClientAssociation(t, f.db, f.owner, f.groupID, results[0].ID, clientID, f.member)
	for _, field := range []string{"body", "reply", "mentions", "all"} {
		changed := input
		switch field {
		case "body":
			changed.Body = "另一个意图"
		case "reply":
			changed.ReplyToMessageID = ""
		case "mentions":
			changed.MentionSubjectIDs = nil
		case "all":
			changed.MentionAll = true
		}
		var conflict *conversationaction.ConflictError
		_, err := send.Execute(ctx, f.owner, changed)
		require.ErrorAs(t, err, &conflict, "changed %s accepted", field)
		require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason, "changed %s accepted", field)
	}
	// 另一个成员使用相同编号创建自己的消息，各自只取得本人关联。
	other, err := send.Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: clientID, Body: input.Body})
	require.NoError(t, err)
	require.NotEqual(t, results[0].ID, other.ID, "other sender")
	require.Equal(t, &clientID, other.ClientMessageID, "other sender")
	assertMemberClientAssociation(t, f.db, f.member, f.groupID, other.ID, clientID, f.owner)
	// 核验成员消息编号的会话归属。
	_, err = directchataction.NewSendFirstDirectTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.WorkspaceIdentity.ID, ClientMessageID: clientID, Body: input.Body})
	var conflict *conversationaction.ConflictError
	require.ErrorAs(t, err, &conflict, "cross-conversation reuse")
	require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason, "cross-conversation reuse")
}

// TestWebsiteClientMessageAssociation 验证访客首发重放、身份隔离以及客服与访客公开契约。
func TestWebsiteClientMessageAssociation(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	visitor := direct.NewWebsiteVisitorBackend(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer, nil, servertest.TestDeployment(t, f.db).S3, servertest.DisabledMail{}, nil)
	const visitorA = "web-session:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const visitorB = "web-session:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	clientID := uuid.NewV7().String()
	input := appservice.WebsiteVisitorTextMessageInput{ClientMessageID: strings.ToUpper(clientID), Body: "相同内容"}
	first, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, input)
	require.NoError(t, err)
	require.Equal(t, &clientID, first.Message.ClientMessageID, "first")
	input.ClientMessageID = clientID
	replay, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, input)
	require.NoError(t, err)
	require.Equal(t, first.Message.ID, replay.Message.ID, "replay")
	require.Equal(t, first.Conversation.ID, replay.Conversation.ID, "replay")
	require.Equal(t, &clientID, replay.Message.ClientMessageID, "replay")
	other, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorB, input)
	require.NoError(t, err)
	require.NotEqual(t, first.Message.ID, other.Message.ID, "other")
	require.NotEqual(t, first.Conversation.ID, other.Conversation.ID, "other")
	require.Equal(t, &clientID, other.Message.ClientMessageID, "other")
	_, err = visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorB, first.Conversation.ID, appservice.WebsiteVisitorMessageHistoryInput{})
	require.Error(t, err, "another visitor read the conversation")
	stored := &servermodels.Message{ID: first.Message.ID}
	require.NoError(t, f.db.NewSelect().Model(stored).WherePK().Scan(ctx))
	require.Equal(t, &clientID, stored.ClientMessageID, "stored")
	count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", first.Conversation.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "replay count")
	// 核验跨访客线程重用编号时的原始发送意图校验。
	threadInput := appservice.WebsiteVisitorTextMessageInput{ClientMessageID: uuid.NewV7().String(), Body: "另一线程"}
	thread, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, threadInput)
	require.NoError(t, err)
	for _, changed := range []appservice.WebsiteVisitorTextMessageInput{
		{ClientMessageID: clientID, Body: "改变正文"},
		{ClientMessageID: clientID, Body: input.Body, ConversationID: &first.Conversation.ID, ReplyToMessageID: first.Message.ID},
		{ClientMessageID: clientID, Body: input.Body, ConversationID: &thread.Conversation.ID},
	} {
		var conflict *appservice.Error
		_, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, changed)
		require.ErrorAs(t, err, &conflict, "changed visitor intent")
		require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason, "changed visitor intent")
	}
	replyInput := servicesessionaction.ServiceTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "客服回复", ReplyToMessageID: first.Message.ID}
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	reply, err := send.Execute(ctx, f.owner, replyInput)
	require.NoError(t, err)
	require.Equal(t, &replyInput.ClientMessageID, reply.ClientMessageID, "reply")
	repeated, err := send.Execute(ctx, f.owner, replyInput)
	require.NoError(t, err)
	require.Equal(t, reply.ID, repeated.ID, "reply replay")
	require.Equal(t, &replyInput.ClientMessageID, repeated.ClientMessageID, "reply replay")
	assertMemberClientAssociation(t, f.db, f.owner, first.Conversation.ID, reply.ID, replyInput.ClientMessageID, f.member)
	page, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, first.Conversation.ID, appservice.WebsiteVisitorMessageHistoryInput{})
	require.NoError(t, err)
	require.Len(t, page.Messages, 3, "visitor history")
	require.Equal(t, &clientID, page.Messages[0].ClientMessageID, "visitor history")
	require.NotNil(t, page.Messages[1].Event, "visitor history")
	require.Nil(t, page.Messages[2].ClientMessageID, "visitor history")
	assertClientAssociationHTTP(t, f.navigationFixture, first.Conversation.ID, first.Message.ID, reply.ID, replyInput.ClientMessageID)
}

// assertClientAssociationHTTP 验证登录会话之间的公开响应隔离且不泄露内部幂等键。
func assertClientAssociationHTTP(t *testing.T, f navigationFixture, conversationID, visitorMessageID, memberMessageID, clientID string) {
	t.Helper()
	service := api.NewService(direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil))
	// 两次独立登录验证本人关联不绑定某个登录令牌。
	ownerEmail := f.owner.Account.Email
	for _, email := range []string{ownerEmail, ownerEmail, f.member.Account.Email} {
		login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, email, "password123")
		request := httptest.NewRequest(http.MethodGet, "/conversations/"+conversationID+"/messages", nil)
		request.Header.Set("Authorization", "Bearer "+login.Token)
		request.Header.Set(appservice.WorkspaceHeader, f.owner.Workspace.ID)
		response := httptest.NewRecorder()
		service.ServeHTTP(response, request)
		var page appservice.ConversationMessageList
		require.Equal(t, http.StatusOK, response.Code, "HTTP body=%s", response.Body.String())
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page), "HTTP body=%s", response.Body.String())
		require.Len(t, page.Messages, 3, "HTTP body=%s", response.Body.String())
		for _, message := range page.Messages {
			// 成员回复领取周期时写入的系统事件不带发送编号。
			if message.Type == domain.MessageTypeSystem {
				require.Nil(t, message.ClientMessageID, "HTTP claim event")
				require.NotNil(t, message.SystemEvent, "HTTP claim event")
				require.Equal(t, domain.ConversationSystemEventServiceSessionClaimed, message.SystemEvent.Type, "HTTP claim event")
				continue
			}
			if message.ID == visitorMessageID || email != ownerEmail {
				require.Nil(t, message.ClientMessageID, "HTTP association leaked")
			} else {
				require.Equal(t, memberMessageID, message.ID, "HTTP sender association")
				require.Equal(t, &clientID, message.ClientMessageID, "HTTP sender association")
			}
		}
		for _, forbidden := range []string{"idempotency", "mmsg:", "chmsg:", "agent:"} {
			require.NotContains(t, response.Body.String(), forbidden, "internal key leaked")
		}
	}
}
