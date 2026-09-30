//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
)

// TestGroupConversationMuted 验证群资料返回当前用户的静音状态。
func TestGroupConversationMuted(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	get := groupchataction.NewGetGroupConversationQuery(f.db)
	mute := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db)
	initial, err := get.Execute(ctx, f.member, f.groupID)
	if err != nil || initial.Muted {
		t.Fatalf("initial group = %#v, error = %v", initial, err)
	}
	for _, muted := range []bool{true, false} {
		if _, err := mute.Execute(ctx, f.member, f.groupID, muted); err != nil {
			t.Fatal(err)
		}
		group, err := get.Execute(ctx, f.member, f.groupID)
		if err != nil || group.Muted != muted {
			t.Fatalf("group muted = %v, want %v, error = %v", group.Muted, muted, err)
		}
		ownerGroup, err := get.Execute(ctx, f.owner, f.groupID)
		if err != nil || ownerGroup.Muted {
			t.Fatalf("member mute affected owner: %#v, error = %v", ownerGroup, err)
		}
	}
}

// TestConversationUnreadMark 验证个人标记、静音和两个阅读水位相互独立。
func TestConversationUnreadMark(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	mark := conversationaction.NewUpdateConversationUnreadMarkAction(f.db)
	read := conversationaction.NewMarkConversationReadAction(f.db)
	review := conversationaction.NewMarkConversationMentionReviewedAction(f.db)
	mute := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db)
	inbox := inboxaction.NewLoadInboxQuery(f.db)
	first := f.send(t, f.owner, "提及", false, f.subjectID)
	if _, err := review.Execute(ctx, f.member, f.groupID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := read.Execute(ctx, f.member, f.groupID, first.ID, false); err != nil {
		t.Fatal(err)
	}
	before := f.state(t)
	for range 2 {
		if err := mark.Execute(ctx, f.member, f.groupID, true); err != nil {
			t.Fatal(err)
		}
	}
	state := f.state(t)
	if !state.MarkedUnread || *state.LastReadMessageID != *before.LastReadMessageID || !state.LastReadAt.Equal(*before.LastReadAt) || *state.LastReviewedMentionMessageID != *before.LastReviewedMentionMessageID {
		t.Fatalf("mark changed read facts: %#v", state)
	}
	rowsPage, counts, err := inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows := rowsPage.Conversations
	if err != nil || len(rows) != 1 || !rows[0].MarkedUnread || counts.Unread != 0 || counts.Attention != 1 {
		t.Fatalf("marked inbox = %#v %#v %v", rows, counts, err)
	}
	f.send(t, f.owner, "普通消息", false)
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || counts.Unread != 1 || counts.Attention != 1 {
		t.Fatalf("mark double counted: %#v %v", counts, err)
	}
	if _, err = mute.Execute(ctx, f.member, f.groupID, true); err != nil {
		t.Fatal(err)
	}
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || counts.Attention != 0 {
		t.Fatalf("muted mark: %#v %v", counts, err)
	}
	last := f.send(t, f.owner, "再次提及", true, f.subjectID)
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || counts.Unread != 2 || counts.Attention != 1 {
		t.Fatalf("muted mention: %#v %v", counts, err)
	}
	if _, err = read.Execute(ctx, f.member, f.groupID, last.ID, false); err != nil {
		t.Fatal(err)
	}
	if !f.state(t).MarkedUnread {
		t.Fatal("automatic read cleared manual mark")
	}
	if _, err = mute.Execute(ctx, f.member, f.groupID, false); err != nil {
		t.Fatal(err)
	}
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || counts.Unread != 0 || counts.Attention != 1 {
		t.Fatalf("manual reminder lost: %#v %v", counts, err)
	}
	if err = mark.Execute(ctx, f.member, f.groupID, false); err != nil {
		t.Fatal(err)
	}
	state = f.state(t)
	if state.MarkedUnread || *state.LastReadMessageID != last.ID || *state.LastReviewedMentionMessageID != first.ID {
		t.Fatalf("clear changed read facts: %#v", state)
	}
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || counts.Unread != 0 || counts.Attention != 0 {
		t.Fatalf("read inbox: %#v %v", counts, err)
	}
	if err = mark.Execute(ctx, f.member, f.groupID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = read.Execute(ctx, f.member, f.groupID, uuid.NewV7().String(), true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("invalid read target = %v", err)
	}
	if !f.state(t).MarkedUnread {
		t.Fatal("failed read cleared unread mark")
	}
	if _, err = read.Execute(ctx, f.member, f.groupID, last.ID, true); err != nil {
		t.Fatal(err)
	}
	if f.state(t).MarkedUnread {
		t.Fatal("explicit read did not clear unread mark at unchanged watermark")
	}

	other := newNavigationFixture(t)
	if err = mark.Execute(ctx, other.member, f.groupID, true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("cross organization = %v", err)
	}
	if _, err = f.db.NewUpdate().Table("conversation_participants").Set("left_at = now()").Where("organization_id = ? AND conversation_id = ? AND subject_id = ?", f.member.Organization.ID, f.groupID, f.subjectID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err = mark.Execute(ctx, f.member, f.groupID, true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("former member = %v", err)
	}
}

// TestDirectConversationUnreadMark 验证单聊的个人标记计入提醒总数，静音后不计入。
func TestDirectConversationUnreadMark(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	sent, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊消息"})
	if err != nil {
		t.Fatal(err)
	}
	inbox := inboxaction.NewLoadInboxQuery(f.db)
	mark := conversationaction.NewUpdateConversationUnreadMarkAction(f.db)
	read := conversationaction.NewMarkConversationReadAction(f.db)
	if _, err = read.Execute(ctx, f.member, sent.Conversation.ID, sent.Message.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = mark.Execute(ctx, f.member, sent.Conversation.ID, true); err != nil {
		t.Fatal(err)
	}
	_, counts, err := inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || counts.Unread != 0 || counts.Attention != 1 {
		t.Fatalf("direct mark: %#v %v", counts, err)
	}
	if _, err = conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, sent.Conversation.ID, true); err != nil {
		t.Fatal(err)
	}
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || counts.Attention != 0 {
		t.Fatalf("muted direct mark: %#v %v", counts, err)
	}
}

// TestEmptyConversationUnreadMarksIgnoreListLimit 验证空群标记、跨筛选汇总和列表条数上限。
func TestEmptyConversationUnreadMarksIgnoreListLimit(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	mark := conversationaction.NewUpdateConversationUnreadMarkAction(f.db)
	create := groupchataction.NewCreateGroupConversationAction(f.db)
	if err := mark.Execute(ctx, f.member, f.groupID, true); err != nil {
		t.Fatal(err)
	}
	state := f.state(t)
	if !state.MarkedUnread || state.LastReadMessageID != nil || state.LastReadAt != nil || state.LastReviewedMentionMessageID != nil {
		t.Fatalf("empty mark created watermarks: %#v", state)
	}
	if err := mark.Execute(ctx, f.member, f.groupID, false); err != nil {
		t.Fatal(err)
	}
	for range 51 {
		group, err := create.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "未读标记测试", MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if err = mark.Execute(ctx, f.member, group.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	inbox := inboxaction.NewLoadInboxQuery(f.db)
	rowsPage, counts, err := inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows := rowsPage.Conversations
	if err != nil || len(rows) != 50 || counts.Unread != 0 || counts.Attention != 51 {
		t.Fatalf("limited list changed total: %d %#v %v", len(rows), counts, err)
	}
	rowsPage, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeAll})
	rows = rowsPage.Conversations
	if err != nil || len(rows) != 0 || counts.Attention != 51 {
		t.Fatalf("service scope changed total: %d %#v %v", len(rows), counts, err)
	}
}
