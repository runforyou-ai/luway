//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestWebsiteVisitorEventsAndRating 验证访客可见的周期事件投影、评价时机、一次性评价与成员侧评价事件。
func TestWebsiteVisitorEventsAndRating(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	const visitor = "web-session:0123456789abcdef0123456789abcdef"
	coordinator := agentrunaction.NewExecuteAction(f.db, nil, nil, testAttachmentReader(f.db), nil, nil)
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, newTestTasks(f.db))
	rate := customerchataction.NewRateWebsiteServiceSessionAction(f.db, newTestTasks(f.db))
	listVisitor := func() customerchataction.MessageHistory {
		t.Helper()
		history, err := customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: f.channelID, ExternalID: visitor, ConversationID: f.conversationID})
		if err != nil {
			t.Fatal(err)
		}
		return history
	}
	var sessionID string
	if err := f.db.NewSelect().Table("service_sessions").Column("id").Where("conversation_id = ?", f.conversationID).Scan(ctx, &sessionID); err != nil {
		t.Fatal(err)
	}
	input := customerchataction.WebsiteServiceSessionRatingInput{ChannelID: f.channelID, ExternalID: visitor, ConversationID: f.conversationID, ServiceSessionID: sessionID, Resolved: true, Comment: "  很快就解决了  "}

	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	_, err := rate.Execute(ctx, input)
	var conflict *conversationaction.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != customerchataction.ConflictReasonServiceSessionNotRateable {
		t.Fatalf("open rating=%v", err)
	}
	if _, err := closeSession.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	reopen := servicesessionaction.NewReopenServiceSessionAction(f.db)
	if _, err := reopen.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if reopened := listVisitor(); len(reopened.SessionRatings) != 0 {
		t.Fatalf("reopened ratings=%+v", reopened.SessionRatings)
	}
	if _, err := closeSession.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	history := listVisitor()
	var events []customerchataction.Message
	for _, message := range history.Messages {
		if message.Author == domain.MessageAuthorSystem {
			events = append(events, message)
		}
	}
	// 重新打开事件不投影给访客。
	if len(events) != 3 || events[0].Event.Type != customerchataction.VisitorEventMemberJoined || events[0].Event.MemberName != f.owner.OrganizationIdentity.DisplayName ||
		events[1].Event.Type != customerchataction.VisitorEventSessionEnded || events[2].Event.Type != customerchataction.VisitorEventSessionEnded {
		t.Fatalf("visitor events=%+v", events)
	}
	if len(history.SessionRatings) != 1 || history.SessionRatings[0].EndMessageID != events[2].ID || !history.SessionRatings[0].Rateable {
		t.Fatalf("closed ratings=%+v", history.SessionRatings)
	}

	foreign := input
	foreign.ExternalID = "web-session:ffffffffffffffffffffffffffffffff"
	if _, err := rate.Execute(ctx, foreign); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("foreign rating=%v", err)
	}
	tooLong := input
	tooLong.Comment = strings.Repeat("长", 1001)
	var validation *conversationaction.ValidationError
	if _, err := rate.Execute(ctx, tooLong); !errors.As(err, &validation) || validation.Fields["comment"] != customerchataction.ValidationRatingCommentTooLong {
		t.Fatalf("long comment=%v", err)
	}

	rating, err := rate.Execute(ctx, input)
	if err != nil || rating.Rateable || rating.Resolved == nil || !*rating.Resolved || rating.Comment != "很快就解决了" {
		t.Fatalf("rating=%+v err=%v", rating, err)
	}
	if _, err := rate.Execute(ctx, input); !errors.As(err, &conflict) || conflict.Reason != customerchataction.ConflictReasonServiceSessionNotRateable {
		t.Fatalf("repeated rating=%v", err)
	}
	session := &servermodels.ServiceSession{}
	if err := f.db.NewSelect().Model(session).Where("ss.id = ?", sessionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if session.RatingResolved == nil || !*session.RatingResolved || session.RatingComment == nil || *session.RatingComment != "很快就解决了" || session.RatedAt == nil {
		t.Fatalf("stored rating=%+v", session)
	}
	rated := listVisitor().SessionRatings
	if len(rated) != 1 || rated[0].Rateable || rated[0].Resolved == nil || !*rated[0].Resolved || rated[0].Comment != "很快就解决了" {
		t.Fatalf("rated visitor state=%+v", rated)
	}
	if _, err := reopen.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if kept := listVisitor().SessionRatings; len(kept) != 1 || kept[0].Rateable {
		t.Fatalf("reopened rated state=%+v", kept)
	}
	member, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil {
		t.Fatal(err)
	}
	ratedEvent := member.Messages[len(member.Messages)-2]
	if ratedEvent.SystemEvent == nil || ratedEvent.SystemEvent.Type != domain.ConversationSystemEventServiceSessionRated || ratedEvent.Visibility != domain.MessageVisibilityInternal ||
		ratedEvent.SystemEvent.Resolved == nil || !*ratedEvent.SystemEvent.Resolved || ratedEvent.SystemEvent.Comment == nil || *ratedEvent.SystemEvent.Comment != "很快就解决了" {
		t.Fatalf("member rated event=%+v", ratedEvent.SystemEvent)
	}

	// 转交给成员时访客看到承接成员加入。
	transfer := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(newTestTasks(f.db)), newTestTasks(f.db))
	if _, err := transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	messages := listVisitor().Messages
	if joined := messages[len(messages)-1].Event; joined == nil || joined.Type != customerchataction.VisitorEventMemberJoined || joined.MemberName != f.member.OrganizationIdentity.DisplayName {
		t.Fatalf("transferred event=%+v", joined)
	}
}
