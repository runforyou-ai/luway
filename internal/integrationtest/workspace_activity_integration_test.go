//go:build server

package integrationtest

import (
	"context"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"testing"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestListWorkspaceAttention 验证各工作区的提醒数量与该工作区收件箱的提醒数量一致，停用的成员身份不再返回。
func TestListWorkspaceAttention(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	backend := newAccountTestBackend(t, f.db)
	token := loginToken(t, f.db, f.owner.Workspace.ID, f.memberEmail)
	second := servertest.AddAccountWorkspace(t, f.db, token, "第二工作区").Workspace
	f.send(t, f.owner, "未读一条", false)

	list, err := backend.ListWorkspaceAttention(ctx, appservice.RequestMeta{Token: token})
	require.NoError(t, err)
	require.Len(t, list.Items, 2, "attention = %#v", list)
	inbox, err := backend.LoadInbox(ctx, appservice.RequestMeta{Token: token, WorkspaceID: f.owner.Workspace.ID}, appservice.LoadInboxInput{Scope: domain.InboxScopeChat, Limit: 1})
	require.NoError(t, err)
	for _, item := range list.Items {
		switch item.WorkspaceID {
		case f.owner.Workspace.ID:
			require.Equal(t, appservice.WorkspaceAttention{
				WorkspaceID: f.owner.Workspace.ID, AttentionUnreadCount: inbox.AttentionUnreadCount,
				PendingCount: inbox.PendingCount, PendingUnreadCount: inbox.PendingUnreadCount,
			}, item, "first workspace attention")
			require.NotZero(t, item.AttentionUnreadCount, "first workspace attention")
		case second.ID:
			require.Equal(t, appservice.WorkspaceAttention{WorkspaceID: second.ID}, item, "second workspace attention")
		default:
			require.Failf(t, "unexpected workspace", "%q", item.WorkspaceID)
		}
	}

	// 停用的成员身份不再计入。
	_, err = testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	list, err = backend.ListWorkspaceAttention(ctx, appservice.RequestMeta{Token: token})
	require.NoError(t, err)
	require.Len(t, list.Items, 1, "attention after deactivation = %#v", list)
	require.Equal(t, second.ID, list.Items[0].WorkspaceID, "attention after deactivation")
}

// TestPlatformDeactivationEndsRealtimeStreams 验证平台管理员停用账号后，该账号已建立的工作区动态事件流随即结束。
func TestPlatformDeactivationEndsRealtimeStreams(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	h := startRealtimeGateway(t, f)
	token := loginToken(t, f.db, f.owner.Workspace.ID, f.memberEmail)
	client := h.connectAccount(t, token)

	// 工作区负责人的账号同时是平台管理员。
	_, err := f.db.NewUpdate().Model((*servermodels.Account)(nil)).Set("is_platform_admin = true").Where("id = ?", f.owner.Account.ID).Exec(ctx)
	require.NoError(t, err)
	operator := &servermodels.AccountIdentity{Account: f.owner.Account}
	_, err = platformaction.NewUpdateAccountAction(f.db).SetStatus(ctx, operator, f.member.Account.ID, domain.AccountStatusInactive)
	require.NoError(t, err)
	client.expectEnded()
}
