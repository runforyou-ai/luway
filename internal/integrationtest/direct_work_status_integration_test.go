//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestDirectPeerWorkStatus 验证单聊摘要返回对端工作状态，且对端修改工作状态时通知本人重读该单聊。
func TestDirectPeerWorkStatus(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	organizationID := f.owner.Organization.ID
	sent, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊工作状态",
	})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := sent.Conversation.ID
	if status := loadDirectPeerWorkStatus(t, f.db, f.owner, conversationID); status != domain.WorkStatusWorking {
		t.Fatalf("初始工作状态 = %q", status)
	}

	feed := startRealtimeFeed(t, organizationID)
	if _, err := useraction.NewUpdateWorkStatusAction(f.db, newTestTasks(f.db)).Execute(ctx, f.member, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusOffDuty}); err != nil {
		t.Fatal(err)
	}
	// 修改者本人收到身份资料通知，单聊对端收到会话变更通知，网站访客收到接待状态变化通知。
	feed.expect(t,
		feed.notice(f.member.User.ID, realtime.KindIdentityProfileChanged, "", loadProfileVersion(t, f.db, f.member.User.ID)),
		feed.notice(f.owner.User.ID, realtime.KindConversationChanged, conversationID, loadConversationVersion(t, f.db, conversationID)),
		feed.reception(),
	)
	if status := loadDirectPeerWorkStatus(t, f.db, f.owner, conversationID); status != domain.WorkStatusOffDuty {
		t.Fatalf("修改后工作状态 = %q", status)
	}
}

// loadDirectPeerWorkStatus 读取指定身份收件箱中该单聊的对端工作状态。
func loadDirectPeerWorkStatus(t *testing.T, db *bun.DB, identity *servermodels.Identity, conversationID string) domain.WorkStatus {
	t.Helper()
	page, _, err := inboxaction.NewLoadInboxQuery(db).Execute(context.Background(), identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Conversations {
		if row.ID == conversationID && row.Direct != nil {
			return row.Direct.PeerWorkStatus
		}
	}
	t.Fatalf("收件箱中没有单聊 %s", conversationID)
	return ""
}
