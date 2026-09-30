//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"uuid"

	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/api"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/appservice/direct"
	"github.com/runforyou-ai/cervi/internal/domain"
	serverfilecontent "github.com/runforyou-ai/cervi/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// assertMemberClientAssociation 核对持久关联和按请求身份隔离的消息窗口。
func assertMemberClientAssociation(t *testing.T, db *bun.DB, sender *servermodels.Identity, conversationID, messageID, clientID string, readers ...*servermodels.Identity) {
	t.Helper()
	ctx := context.Background()
	stored := &servermodels.Message{ID: messageID}
	if err := db.NewSelect().Model(stored).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.ClientMessageID == nil || *stored.ClientMessageID != clientID || stored.SenderParticipantID == nil || stored.IdempotencyKey == nil || *stored.IdempotencyKey == clientID {
		t.Fatalf("missing independent sender association: %+v", stored)
	}
	for _, reader := range append([]*servermodels.Identity{sender}, readers...) {
		page, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, reader, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, AroundMessageID: messageID})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range page.Messages {
			if message.ID != messageID {
				continue
			}
			found = true
			if reader.OrganizationIdentity.ID == sender.OrganizationIdentity.ID {
				if message.ClientMessageID == nil || *message.ClientMessageID != clientID {
					t.Fatalf("sender association=%+v", message)
				}
			} else if message.ClientMessageID != nil {
				t.Fatalf("association leaked to another member: %+v", message)
			}
		}
		if !found {
			t.Fatalf("message %s missing", messageID)
		}
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
		if failures[i] != nil || result.ID != results[0].ID || result.ClientMessageID == nil || *result.ClientMessageID != clientID {
			t.Fatalf("replay %d=%+v err=%v", i, result, failures[i])
		}
	}
	if count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ? AND client_message_id = ?", f.groupID, clientID).Count(ctx); err != nil || count != 1 {
		t.Fatalf("replay count=%d err=%v", count, err)
	}
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
		if _, err := send.Execute(ctx, f.owner, changed); !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
			t.Fatalf("changed %s accepted: %v", field, err)
		}
	}
	// 另一个成员使用相同编号创建自己的消息，各自只取得本人关联。
	other, err := send.Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: clientID, Body: input.Body})
	if err != nil || other.ID == results[0].ID || other.ClientMessageID == nil || *other.ClientMessageID != clientID {
		t.Fatalf("other sender=%+v err=%v", other, err)
	}
	assertMemberClientAssociation(t, f.db, f.member, f.groupID, other.ID, clientID, f.owner)
	// 核验成员消息编号的会话归属。
	_, err = directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: clientID, Body: input.Body})
	var conflict *conversationaction.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
		t.Fatalf("cross-conversation reuse=%v", err)
	}
}

// TestDirectClientMessageAssociation 验证单聊首次发送和后续发送的响应、重放及窗口关联。
func TestDirectClientMessageAssociation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	input := directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "首次发送"}
	start := directchataction.NewSendFirstDirectTextMessageAction(f.db)
	first, err := start.Execute(ctx, f.owner, input)
	if err != nil || first.Message.ClientMessageID == nil || *first.Message.ClientMessageID != input.ClientMessageID {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replay, err := start.Execute(ctx, f.owner, input)
	if err != nil || replay.Message.ID != first.Message.ID || replay.Message.ClientMessageID == nil || *replay.Message.ClientMessageID != input.ClientMessageID {
		t.Fatalf("first replay=%+v err=%v", replay, err)
	}
	assertMemberClientAssociation(t, f.db, f.owner, first.Conversation.ID, first.Message.ID, input.ClientMessageID, f.member)
	send := directchataction.NewSendDirectTextMessageAction(f.db)
	nextInput := directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "后续发送", ReplyToMessageID: first.Message.ID}
	next, err := send.Execute(ctx, f.member, nextInput)
	if err != nil || next.ClientMessageID == nil || *next.ClientMessageID != nextInput.ClientMessageID {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	repeated, err := send.Execute(ctx, f.member, nextInput)
	if err != nil || repeated.ID != next.ID || repeated.ClientMessageID == nil || *repeated.ClientMessageID != nextInput.ClientMessageID {
		t.Fatalf("next replay=%+v err=%v", repeated, err)
	}
	assertMemberClientAssociation(t, f.db, f.member, first.Conversation.ID, next.ID, nextInput.ClientMessageID, f.owner)
}

// TestWebsiteClientMessageAssociation 验证访客首发重放、身份隔离以及客服与访客公开契约。
func TestWebsiteClientMessageAssociation(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	visitor := direct.NewWebsiteVisitorBackend(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), newTestTasks(f.db), nil, serverfilecontent.S3Config{}, nil, nil)
	const visitorA = "web-session:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const visitorB = "web-session:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	clientID := uuid.NewV7().String()
	input := appservice.WebsiteVisitorTextMessageInput{ClientMessageID: strings.ToUpper(clientID), Body: "相同内容"}
	first, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, input)
	if err != nil || first.Message.ClientMessageID == nil || *first.Message.ClientMessageID != clientID {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	input.ClientMessageID = clientID
	replay, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, input)
	if err != nil || replay.Message.ID != first.Message.ID || replay.Conversation.ID != first.Conversation.ID || replay.Message.ClientMessageID == nil || *replay.Message.ClientMessageID != clientID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	other, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorB, input)
	if err != nil || other.Message.ID == first.Message.ID || other.Conversation.ID == first.Conversation.ID || other.Message.ClientMessageID == nil || *other.Message.ClientMessageID != clientID {
		t.Fatalf("other=%+v err=%v", other, err)
	}
	if _, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorB, first.Conversation.ID, appservice.WebsiteVisitorMessageHistoryInput{}); err == nil {
		t.Fatal("another visitor read the conversation")
	}
	stored := &servermodels.Message{ID: first.Message.ID}
	if err := f.db.NewSelect().Model(stored).WherePK().Scan(ctx); err != nil || stored.ClientMessageID == nil || *stored.ClientMessageID != clientID {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", first.Conversation.ID).Count(ctx); err != nil || count != 1 {
		t.Fatalf("replay count=%d err=%v", count, err)
	}
	// 核验跨访客线程重用编号时的原始发送意图校验。
	threadInput := appservice.WebsiteVisitorTextMessageInput{ClientMessageID: uuid.NewV7().String(), Body: "另一线程"}
	thread, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, threadInput)
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []appservice.WebsiteVisitorTextMessageInput{
		{ClientMessageID: clientID, Body: "改变正文"},
		{ClientMessageID: clientID, Body: input.Body, ConversationID: &first.Conversation.ID, ReplyToMessageID: first.Message.ID},
		{ClientMessageID: clientID, Body: input.Body, ConversationID: &thread.Conversation.ID},
	} {
		var conflict *appservice.Error
		if _, err := visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, changed); !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
			t.Fatalf("changed visitor intent=%v", err)
		}
	}
	replyInput := servicesessionaction.ServiceTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "客服回复", ReplyToMessageID: first.Message.ID}
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	reply, err := send.Execute(ctx, f.owner, replyInput)
	if err != nil || reply.ClientMessageID == nil || *reply.ClientMessageID != replyInput.ClientMessageID {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
	repeated, err := send.Execute(ctx, f.owner, replyInput)
	if err != nil || repeated.ID != reply.ID || repeated.ClientMessageID == nil || *repeated.ClientMessageID != replyInput.ClientMessageID {
		t.Fatalf("reply replay=%+v err=%v", repeated, err)
	}
	assertMemberClientAssociation(t, f.db, f.owner, first.Conversation.ID, reply.ID, replyInput.ClientMessageID, f.member)
	page, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, visitorA, first.Conversation.ID, appservice.WebsiteVisitorMessageHistoryInput{})
	if err != nil || len(page.Messages) != 3 || page.Messages[0].ClientMessageID == nil || *page.Messages[0].ClientMessageID != clientID || page.Messages[1].Event == nil || page.Messages[2].ClientMessageID != nil {
		t.Fatalf("visitor history=%+v err=%v", page, err)
	}
	assertClientAssociationHTTP(t, f.navigationFixture, first.Conversation.ID, first.Message.ID, reply.ID, replyInput.ClientMessageID)
}

// assertClientAssociationHTTP 验证登录会话之间的公开响应隔离且不泄露内部幂等键。
func assertClientAssociationHTTP(t *testing.T, f navigationFixture, conversationID, visitorMessageID, memberMessageID, clientID string) {
	t.Helper()
	service := api.NewService(appservice.New(direct.New(f.db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)))
	// 两次独立登录验证本人关联不绑定某个登录令牌。
	ownerEmail := f.owner.Account.Email
	for _, email := range []string{ownerEmail, ownerEmail, f.member.Account.Email} {
		login := loginMember(t, f.db, f.owner.Organization.ID, email, "password123")
		request := httptest.NewRequest(http.MethodGet, "/conversations/"+conversationID+"/messages", nil)
		request.Header.Set("Authorization", "Bearer "+login.Token)
		request.Header.Set(appservice.WorkspaceHeader, f.owner.Organization.ID)
		response := httptest.NewRecorder()
		service.ServeHTTP(response, request)
		var page appservice.ConversationMessageList
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Messages) != 3 {
			t.Fatalf("HTTP status=%d body=%s", response.Code, response.Body.String())
		}
		for _, message := range page.Messages {
			// 成员回复领取周期时写入的系统事件不带发送编号。
			if message.Type == appservice.MessageTypeSystem {
				if message.ClientMessageID != nil || message.SystemEvent == nil || message.SystemEvent.Type != appservice.ConversationSystemEventServiceSessionClaimed {
					t.Fatalf("HTTP claim event=%+v", message)
				}
				continue
			}
			if message.ID == visitorMessageID || email != ownerEmail {
				if message.ClientMessageID != nil {
					t.Fatalf("HTTP association leaked: %+v", message)
				}
			} else if message.ID != memberMessageID || message.ClientMessageID == nil || *message.ClientMessageID != clientID {
				t.Fatalf("HTTP sender association=%+v", message)
			}
		}
		for _, forbidden := range []string{"idempotency", "mmsg:", "chmsg:", "agent:"} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("internal key leaked: %s", response.Body.String())
			}
		}
	}
}
