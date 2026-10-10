//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	memberaction "github.com/runforyou-ai/luway/internal/actions/member"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/stretchr/testify/require"
)

// TestPersonalAgentDirectory 验证 AI 员工目录只列出服务型 AI 员工与本人负责的个人 AI 员工，启停他人负责的个人 AI 员工需要工作区管理权限，个人 AI 员工不进入成员候选、写回复候选和服务型 AI 员工的管理操作。
func TestPersonalAgentDirectory(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	member := newChatLockUser(t, db, identity)
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}}
	service, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "目录服务员工", ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceEmployee}, Execution: execution,
	})
	require.NoError(t, err)
	_, err = agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, service.ID, agentaction.UpdateInput{
		DisplayName: service.DisplayName, ServiceAudiences: service.ServiceAudiences, ResponsibleUserID: member.User.ID, WorkStatus: domain.WorkStatusWorking,
	})
	require.NoError(t, err)
	createPersonal := func(owner *servermodels.Identity, name string) *agentaction.PersonalAgent {
		t.Helper()
		computer, err := computeraction.NewRegisterComputerAction(db).Execute(ctx, owner, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: name + "的电脑"})
		require.NoError(t, err)
		created, err := agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, owner, computer.Record.ID, agentaction.PersonalAgentInput{DisplayName: name, Execution: execution})
		require.NoError(t, err)
		return created
	}
	own := createPersonal(member, "目录本人员工")
	other := createPersonal(identity, "目录他人员工")

	t.Run("目录列出服务型与本人负责的个人 AI 员工", func(t *testing.T) {
		output, err := agentaction.NewListAgentsQuery(db).Execute(ctx, member, agentaction.ListInput{Query: "目录", Page: 1, PageSize: 50})
		require.NoError(t, err)
		ids := make([]string, 0, len(output.Agents))
		for _, item := range output.Agents {
			ids = append(ids, item.ID)
			switch item.ID {
			case own.ID:
				require.True(t, item.Personal(), "own personal item=%+v", item)
				require.Equal(t, own.ComputerID, support.Deref(item.ComputerID))
				require.Equal(t, domain.PersonalAgentPresenceOffline, item.Presence())
			case service.ID:
				require.False(t, item.Personal(), "service item=%+v", item)
				require.Nil(t, item.ComputerID)
			}
		}
		require.Contains(t, ids, service.ID)
		require.Contains(t, ids, own.ID)
		require.NotContains(t, ids, other.ID)
		require.Equal(t, 2, output.Page.Total)
	})

	t.Run("个人 AI 员工不进入成员候选与写回复候选", func(t *testing.T) {
		options, err := memberaction.NewListOptionsQuery(db).Execute(ctx, member, memberaction.ListOptionsInput{Query: "目录", Page: 1, PageSize: 50})
		require.NoError(t, err)
		require.Len(t, options.Members, 1)
		require.Equal(t, service.IdentityID, options.Members[0].ID)
		replyAgents, err := agentrunaction.NewListServiceReplyAgentsQuery(db).Execute(ctx, member)
		require.NoError(t, err)
		for _, agent := range replyAgents {
			require.NotEqual(t, own.IdentityID, agent.IdentityID, "reply agents include personal")
			require.NotEqual(t, other.IdentityID, agent.IdentityID, "reply agents include personal")
		}
	})

	t.Run("服务型 AI 员工的管理操作不作用于个人 AI 员工", func(t *testing.T) {
		_, err := agentaction.NewGetAgentQuery(db).Execute(ctx, member, own.ID)
		require.ErrorIs(t, err, agentaction.ErrNotFound, "get personal as service")
		_, err = agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, member, own.ID, agentaction.UpdateInput{
			DisplayName: own.DisplayName, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, WorkStatus: domain.WorkStatusWorking,
		})
		require.ErrorIs(t, err, agentaction.ErrNotFound, "update personal as service")
		_, _, err = agentaction.NewGetPersonalAgentQuery(db).Execute(ctx, member, other.ID)
		require.ErrorIs(t, err, agentaction.ErrPersonalAgentNotFound, "get other's personal")
		_, _, err = agentaction.NewGetPersonalAgentQuery(db).Execute(ctx, member, service.ID)
		require.ErrorIs(t, err, agentaction.ErrPersonalAgentNotFound, "get service as personal")
	})

	t.Run("启停他人负责的个人 AI 员工需要工作区管理权限", func(t *testing.T) {
		var memberRoleID string
		require.NoError(t, db.NewSelect().Model((*servermodels.Role)(nil)).ColumnExpr("id::text").
			Where("workspace_id = ? AND kind = ?", identity.Workspace.ID, domain.RoleKindMember).Scan(ctx, &memberRoleID))
		email := servertest.UniqueEmail("personal-status-viewer")
		_, err := newTestMemberCreator(db, testEnqueuer).Execute(ctx, identity, memberSpec{DisplayName: "普通成员", Email: email, Password: "password123", RoleID: memberRoleID})
		require.NoError(t, err)
		viewer := servertest.LoginMember(t, db, identity.Workspace.ID, email, "password123").Identity
		status := agentaction.NewUpdatePersonalAgentStatusAction(db, testEnqueuer)
		_, err = status.Execute(ctx, viewer, other.ID, domain.IdentityStatusInactive)
		require.ErrorIs(t, err, agentaction.ErrPersonalAgentForbidden)
		viewerOwn := createPersonal(viewer, "目录成员自己的员工")
		_, err = status.Execute(ctx, viewer, viewerOwn.ID, domain.IdentityStatusInactive)
		require.NoError(t, err)
		_, err = status.Execute(ctx, identity, other.ID, domain.IdentityStatusInactive)
		require.NoError(t, err)
	})
}
