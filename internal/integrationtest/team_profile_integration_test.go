//go:build server

package integrationtest

import (
	"context"
	"testing"

	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/domain"
)

// TestTeamRenameInvalidatesCustomerInbox 验证团队改名推进会话版本与同步探针并通知客服受众，相同名称和描述变更保持版本。
func TestTeamRenameInvalidatesCustomerInbox(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "售前团队"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Table("service_sessions").Set("team_id = ?", team.ID).
		Where("organization_id = ? AND conversation_id = ?", f.owner.Organization.ID, f.conversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	feed := startRealtimeFeed(t, f.owner.Organization.ID)
	query := inboxaction.NewLoadInboxQuery(f.db)
	before, err := query.SyncHeads(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	version := loadConversationVersion(t, f.db, f.conversationID)
	update := teamaction.NewUpdateTeamAction(f.db)
	if _, err := update.Execute(ctx, f.member, team.ID, teamaction.Input{Name: "客户成功团队"}); err != nil {
		t.Fatal(err)
	}
	after, err := query.SyncHeads(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if before.ConversationChecksum == after.ConversationChecksum || before.ConversationCount != after.ConversationCount {
		t.Fatalf("改名后同步探针 = %+v, 改名前 = %+v", after, before)
	}
	row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen)
	if row.Service.TeamName == nil || *row.Service.TeamName != "客户成功团队" {
		t.Fatalf("收件箱团队名称 = %v", row.Service.TeamName)
	}
	if current := loadConversationVersion(t, f.db, f.conversationID); current != version+1 {
		t.Fatalf("改名后会话版本 = %d, want %d", current, version+1)
	}
	feed.expect(t, feed.customerInbox(f.conversationID, version+1))
	for _, description := range []string{"", "团队说明"} {
		if _, err := update.Execute(ctx, f.owner, team.ID, teamaction.Input{Name: "客户成功团队", Description: description}); err != nil {
			t.Fatal(err)
		}
		if current := loadConversationVersion(t, f.db, f.conversationID); current != version+1 {
			t.Fatalf("名称未变时推进了会话版本: %d", current)
		}
	}
}
