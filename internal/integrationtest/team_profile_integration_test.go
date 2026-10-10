//go:build server

package integrationtest

import (
	"context"
	"testing"

	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/stretchr/testify/require"
)

// TestTeamRenameInvalidatesCustomerInbox 验证团队改名推进会话版本与同步探针并通知客服受众，相同名称和描述变更保持版本。
func TestTeamRenameInvalidatesCustomerInbox(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "售前团队"})
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Table("service_sessions").Set("team_id = ?", team.ID).
		Where("workspace_id = ? AND conversation_id = ?", f.owner.Workspace.ID, f.conversationID).Exec(ctx)
	require.NoError(t, err)
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	query := inboxaction.NewLoadInboxQuery(f.db)
	before, err := query.SyncHeads(ctx, f.owner)
	require.NoError(t, err)
	version := loadConversationVersion(t, f.db, f.conversationID)
	update := teamaction.NewUpdateTeamAction(f.db)
	_, err = update.Execute(ctx, f.member, team.ID, teamaction.Input{Name: "客户成功团队"})
	require.NoError(t, err)
	after, err := query.SyncHeads(ctx, f.owner)
	require.NoError(t, err)
	require.NotEqual(t, before.ConversationChecksum, after.ConversationChecksum, "改名后同步探针")
	require.Equal(t, before.ConversationCount, after.ConversationCount, "改名后同步探针")
	row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen)
	require.NotNil(t, row.Service.TeamName)
	require.Equal(t, "客户成功团队", *row.Service.TeamName)
	require.Equal(t, version+1, loadConversationVersion(t, f.db, f.conversationID), "改名后会话版本")
	feed.expect(t, feed.customerInbox(f.conversationID, version+1))
	for _, description := range []string{"", "团队说明"} {
		_, err := update.Execute(ctx, f.owner, team.ID, teamaction.Input{Name: "客户成功团队", Description: description})
		require.NoError(t, err)
		require.Equal(t, version+1, loadConversationVersion(t, f.db, f.conversationID), "名称未变时推进了会话版本")
	}
}
