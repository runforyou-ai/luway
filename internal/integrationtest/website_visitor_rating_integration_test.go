//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
)

// TestWebsiteVisitorEventsAndRating 验证访客可见的周期事件投影、评价时机、一次性评价与成员侧评价事件。
func TestWebsiteVisitorEventsAndRating(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	const visitor = "web-session:0123456789abcdef0123456789abcdef"
	coordinator := newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer)
	rate := customerchataction.NewRateWebsiteServiceSessionAction(f.db, testEnqueuer)
	listVisitor := func() customerchataction.MessageHistory {
		t.Helper()
		history, err := customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: f.channelID, ExternalID: visitor, ConversationID: f.conversationID})
		require.NoError(t, err)
		return history
	}
	var sessionID string
	require.NoError(t, f.db.NewSelect().Table("service_sessions").Column("id").Where("conversation_id = ?", f.conversationID).Scan(ctx, &sessionID))
	input := customerchataction.WebsiteServiceSessionRatingInput{ChannelID: f.channelID, ExternalID: visitor, ConversationID: f.conversationID, ServiceSessionID: sessionID, Resolved: true, Comment: "  很快就解决了  "}

	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = rate.Execute(ctx, input)
	var conflict *conversationaction.ConflictError
	require.ErrorAs(t, err, &conflict, "open rating")
	require.Equal(t, customerchataction.ConflictReasonServiceSessionNotRateable, conflict.Reason, "open rating")
	_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	reopen := servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer)
	_, err = reopen.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	require.Empty(t, listVisitor().SessionRatings, "reopened ratings")
	_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	history := listVisitor()
	events := arr.Filter(history.Messages, func(message customerchataction.Message) bool { return message.Author == domain.MessageAuthorSystem })
	// 重新打开事件不投影给访客。
	require.Len(t, events, 3, "visitor events=%+v", events)
	require.Equal(t, customerchataction.VisitorEventMemberJoined, events[0].Event.Type, "visitor events=%+v", events)
	require.Equal(t, f.owner.WorkspaceIdentity.DisplayName, events[0].Event.MemberName, "visitor events=%+v", events)
	require.Equal(t, customerchataction.VisitorEventSessionEnded, events[1].Event.Type, "visitor events=%+v", events)
	require.Equal(t, customerchataction.VisitorEventSessionEnded, events[2].Event.Type, "visitor events=%+v", events)
	require.Len(t, history.SessionRatings, 1, "closed ratings")
	require.Equal(t, events[2].ID, history.SessionRatings[0].EndMessageID, "closed ratings")
	require.True(t, history.SessionRatings[0].Rateable, "closed ratings")

	foreign := input
	foreign.ExternalID = "web-session:ffffffffffffffffffffffffffffffff"
	_, err = rate.Execute(ctx, foreign)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "foreign rating")
	tooLong := input
	tooLong.Comment = strings.Repeat("长", 1001)
	var validation *conversationaction.ValidationError
	_, err = rate.Execute(ctx, tooLong)
	require.ErrorAs(t, err, &validation, "long comment")
	require.Equal(t, customerchataction.ValidationRatingCommentTooLong, validation.Fields["comment"], "long comment")

	rating, err := rate.Execute(ctx, input)
	require.NoError(t, err)
	require.False(t, rating.Rateable)
	require.NotNil(t, rating.Resolved)
	require.True(t, *rating.Resolved)
	require.Equal(t, "很快就解决了", rating.Comment)
	_, err = rate.Execute(ctx, input)
	require.ErrorAs(t, err, &conflict, "repeated rating")
	require.Equal(t, customerchataction.ConflictReasonServiceSessionNotRateable, conflict.Reason, "repeated rating")
	session := &servermodels.ServiceSession{}
	require.NoError(t, f.db.NewSelect().Model(session).Where("ss.id = ?", sessionID).Scan(ctx))
	require.NotNil(t, session.RatingResolved, "stored rating")
	require.True(t, *session.RatingResolved, "stored rating")
	require.NotNil(t, session.RatingComment, "stored rating")
	require.Equal(t, "很快就解决了", *session.RatingComment, "stored rating")
	require.NotNil(t, session.RatedAt, "stored rating")
	rated := listVisitor().SessionRatings
	require.Len(t, rated, 1, "rated visitor state=%+v", rated)
	require.False(t, rated[0].Rateable, "rated visitor state=%+v", rated)
	require.NotNil(t, rated[0].Resolved, "rated visitor state=%+v", rated)
	require.True(t, *rated[0].Resolved, "rated visitor state=%+v", rated)
	require.Equal(t, "很快就解决了", rated[0].Comment, "rated visitor state=%+v", rated)
	_, err = reopen.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	kept := listVisitor().SessionRatings
	require.Len(t, kept, 1, "reopened rated state=%+v", kept)
	require.False(t, kept[0].Rateable, "reopened rated state=%+v", kept)
	member, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	ratedEvent := member.Messages[len(member.Messages)-2]
	require.NotNil(t, ratedEvent.SystemEvent)
	require.Equal(t, domain.ConversationSystemEventServiceSessionRated, ratedEvent.SystemEvent.Type)
	require.Equal(t, domain.MessageVisibilityInternal, ratedEvent.Visibility)
	require.NotNil(t, ratedEvent.SystemEvent.Resolved)
	require.True(t, *ratedEvent.SystemEvent.Resolved)
	require.NotNil(t, ratedEvent.SystemEvent.Comment)
	require.Equal(t, "很快就解决了", *ratedEvent.SystemEvent.Comment)

	// 转交给成员时访客看到承接成员加入。
	transfer := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	_, err = transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	messages := listVisitor().Messages
	joined := messages[len(messages)-1].Event
	require.NotNil(t, joined, "transferred event")
	require.Equal(t, customerchataction.VisitorEventMemberJoined, joined.Type, "transferred event")
	require.Equal(t, f.member.WorkspaceIdentity.DisplayName, joined.MemberName, "transferred event")
}
