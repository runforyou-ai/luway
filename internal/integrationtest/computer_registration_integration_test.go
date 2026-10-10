//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// computerFixture 是电脑注册测试的企业、电脑所属成员和另一名成员。
type computerFixture struct {
	db     *bun.DB
	owner  *servermodels.Identity
	member *servermodels.Identity
}

// newComputerFixture 初始化企业并创建一名普通成员。
func newComputerFixture(t *testing.T) computerFixture {
	t.Helper()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	suffix := uuid.NewV7().String()
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: "电脑测试", DisplayName: "电脑所属成员",
		Email: "owner@" + suffix + ".computer.test", Password: "password123", Locale: domain.LocaleEnglishUnitedStates, TimeZone: "UTC",
	})
	owner := installed.Identity
	memberEmail := "member@" + suffix + ".computer.test"
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{
		DisplayName: "另一名成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID,
	})
	require.NoError(t, err)
	login := servertest.LoginMember(t, db, owner.Workspace.ID, memberEmail, "password123")
	return computerFixture{db: db, owner: owner, member: login.Identity}
}

// TestComputerRegistrationIsIdempotentPerInstall 验证同一安装重复注册指向同一台电脑并更新上报信息，重新注册后只有新凭据有效。
func TestComputerRegistrationIsIdempotentPerInstall(t *testing.T) {
	t.Parallel()
	f := newComputerFixture(t)
	ctx := context.Background()
	register := computeraction.NewRegisterComputerAction(f.db)
	installID := uuid.NewV7().String()

	first, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: installID, Name: "工作本",
	})
	require.NoError(t, err)
	second, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: installID, Name: "改名后的工作本",
	})
	require.NoError(t, err)
	require.Equal(t, first.Record.ID, second.Record.ID, "同一安装注册出两台电脑")
	authenticate := computeraction.NewAuthenticateComputerQuery(f.db)
	_, err = authenticate.Execute(ctx, first.Credential)
	require.ErrorIs(t, err, computeraction.ErrCredentialInvalid, "旧凭据认证")
	identity, err := authenticate.Execute(ctx, second.Credential)
	require.NoError(t, err, "新凭据认证")
	require.Equal(t, first.Record.ID, identity.ComputerID)
	require.Equal(t, f.owner.Workspace.ID, identity.WorkspaceID)
	require.Equal(t, "改名后的工作本", second.Record.Name, "重复注册未更新上报的名称")
	require.Len(t, listComputers(t, f.db, f.owner), 1)
}

// TestComputerRegistrationSeparatesUsers 验证电脑属于注册它的成员，其他成员既看不到也撤销不了。
func TestComputerRegistrationSeparatesUsers(t *testing.T) {
	t.Parallel()
	f := newComputerFixture(t)
	ctx := context.Background()
	register := computeraction.NewRegisterComputerAction(f.db)
	ownerComputer, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: uuid.NewV7().String(), Name: "负责人的电脑",
	})
	require.NoError(t, err)
	_, err = register.Execute(ctx, f.member, computeraction.RegisterInput{
		InstallID: uuid.NewV7().String(), Name: "成员的电脑",
	})
	require.NoError(t, err)

	computers := listComputers(t, f.db, f.member)
	require.Len(t, computers, 1)
	require.Equal(t, "成员的电脑", computers[0].Name)
	err = computeraction.NewRevokeComputerAction(f.db, newTestToolDecisions(f.db, testEnqueuer)).Execute(ctx, f.member, ownerComputer.Record.ID)
	require.ErrorIs(t, err, computeraction.ErrNotFound, "成员撤销他人电脑")
}

// TestComputerRevocationHidesComputerUntilRegisteredAgain 验证撤销后电脑离开列表且凭据失效，同一安装重新注册即恢复原电脑。
func TestComputerRevocationHidesComputerUntilRegisteredAgain(t *testing.T) {
	t.Parallel()
	f := newComputerFixture(t)
	ctx := context.Background()
	register := computeraction.NewRegisterComputerAction(f.db)
	revoke := computeraction.NewRevokeComputerAction(f.db, newTestToolDecisions(f.db, testEnqueuer))
	installID := uuid.NewV7().String()
	registered, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: installID, Name: "工作本",
	})
	require.NoError(t, err)

	require.NoError(t, revoke.Execute(ctx, f.owner, registered.Record.ID))
	_, err = computeraction.NewAuthenticateComputerQuery(f.db).Execute(ctx, registered.Credential)
	require.ErrorIs(t, err, computeraction.ErrCredentialInvalid, "撤销后凭据认证")
	require.Empty(t, listComputers(t, f.db, f.owner), "撤销后电脑列表")
	require.ErrorIs(t, revoke.Execute(ctx, f.owner, registered.Record.ID), computeraction.ErrNotFound, "重复撤销")

	again, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: installID, Name: "工作本",
	})
	require.NoError(t, err)
	require.Equal(t, registered.Record.ID, again.Record.ID, "重新注册产生新电脑")
	require.Len(t, listComputers(t, f.db, f.owner), 1, "重新注册后电脑列表")
}

// TestWorkspaceComputerLifecycle 验证工作区电脑的添加、凭据认证、平台上报、重置凭据与撤销：添加者以外拥有工作区管理权限的成员也能管理，
// 不出现在个人电脑列表，凭据不随成员状态失效，撤销时解除 AI 员工的绑定。
func TestWorkspaceComputerLifecycle(t *testing.T) {
	t.Parallel()
	f := newComputerFixture(t)
	ctx := context.Background()
	create := computeraction.NewCreateWorkspaceComputerAction(f.db)
	authenticate := computeraction.NewAuthenticateComputerQuery(f.db)

	added, err := create.Execute(ctx, f.owner, " 构建服务器 ")
	require.NoError(t, err)
	require.Equal(t, domain.ComputerKindWorkspace, added.Record.Kind)
	require.Equal(t, "构建服务器", added.Record.Name)
	require.Nil(t, added.Record.Platform)
	computer, err := authenticate.Execute(ctx, added.Credential)
	require.NoError(t, err, "工作区电脑认证")
	require.Equal(t, added.Record.ID, computer.ComputerID)

	// 平台随执行能力上报，无效平台记为空。
	report := computeraction.NewReportCapabilitiesAction(f.db)
	require.NoError(t, report.Execute(ctx, computer, computeraction.CapabilitiesInput{Platform: domain.ComputerPlatformLinux}))
	listed := listWorkspaceComputers(t, f.db, f.member)
	require.Len(t, listed, 1)
	require.NotNil(t, listed[0].Platform)
	require.Equal(t, domain.ComputerPlatformLinux, *listed[0].Platform)
	require.NoError(t, report.Execute(ctx, computer, computeraction.CapabilitiesInput{Platform: "ios"}))
	require.Nil(t, listWorkspaceComputers(t, f.db, f.owner)[0].Platform, "无效平台上报后的平台")
	require.Empty(t, listComputers(t, f.db, f.owner), "个人电脑列表包含工作区电脑")

	// 添加者停用后工作区电脑的凭据仍然有效。
	_, err = f.db.NewUpdate().Model((*servermodels.User)(nil)).Set("status = ?", domain.IdentityStatusInactive).
		Where("id = ?", f.owner.User.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = authenticate.Execute(ctx, added.Credential)
	require.NoError(t, err, "添加者停用后的认证")

	// 拥有工作区管理权限的其他成员可以重置凭据，旧凭据立即失效。
	reset, err := computeraction.NewResetComputerCredentialAction(f.db, newTestToolDecisions(f.db, testEnqueuer)).Execute(ctx, f.member, added.Record.ID)
	require.NoError(t, err)
	_, err = authenticate.Execute(ctx, added.Credential)
	require.ErrorIs(t, err, computeraction.ErrCredentialInvalid, "重置后旧凭据认证")
	computer, err = authenticate.Execute(ctx, reset.Credential)
	require.NoError(t, err, "重置后新凭据认证")
	require.Equal(t, added.Record.ID, computer.ComputerID)

	// 撤销后离开列表、凭据失效，个人电脑不能重置凭据。
	require.NoError(t, computeraction.NewRevokeComputerAction(f.db, newTestToolDecisions(f.db, testEnqueuer)).Execute(ctx, f.member, added.Record.ID))
	require.Empty(t, listWorkspaceComputers(t, f.db, f.member), "撤销后的工作区电脑列表")
	_, err = authenticate.Execute(ctx, reset.Credential)
	require.ErrorIs(t, err, computeraction.ErrCredentialInvalid, "撤销后认证")
	personal, err := computeraction.NewRegisterComputerAction(f.db).Execute(ctx, f.member, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "成员的电脑"})
	require.NoError(t, err)
	_, err = computeraction.NewResetComputerCredentialAction(f.db, newTestToolDecisions(f.db, testEnqueuer)).Execute(ctx, f.member, personal.Record.ID)
	require.ErrorIs(t, err, computeraction.ErrNotFound, "重置个人电脑凭据")
}

// TestWorkspaceComputerBinding 验证服务型 AI 员工绑定工作区电脑：服务员工与客户时都可以绑定并保存授权，缺少有效授权、
// 个人电脑与已撤销的电脑都按字段校验失败，撤销电脑时解除绑定。
func TestWorkspaceComputerBinding(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "运维助手", ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceEmployee},
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}},
	})
	require.NoError(t, err)
	added, err := computeraction.NewCreateWorkspaceComputerAction(db).Execute(ctx, identity, "构建服务器")
	require.NoError(t, err)
	personal, err := computeraction.NewRegisterComputerAction(db).Execute(ctx, identity, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "个人电脑"})
	require.NoError(t, err)
	grant := domain.ToolGrant{MaxLevel: domain.OperationLevelL2, ConfirmL2: true}
	update := func(computerID string, audiences ...domain.ServiceAudience) (*agentaction.Agent, error) {
		return agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, agent.ID, agentaction.UpdateInput{
			DisplayName: agent.DisplayName, TeamIDs: []string{}, ServiceAudiences: audiences, ComputerID: computerID, ComputerGrant: grant, WorkStatus: domain.WorkStatusWorking,
		})
	}
	fieldCode := func(err error) common.FieldCode {
		var fieldError *common.FieldError
		if !errors.As(err, &fieldError) {
			return ""
		}
		return fieldError.Fields["computerId"] + fieldError.Fields["computerGrant"]
	}

	updated, err := update(added.Record.ID, domain.ServiceAudienceEmployee)
	require.NoError(t, err, "绑定工作区电脑")
	require.NotNil(t, updated.Computer)
	require.Equal(t, added.Record.ID, updated.Computer.ID)
	require.Equal(t, "构建服务器", updated.Computer.Name)
	require.Equal(t, grant, updated.Computer.Grant)
	listed := listWorkspaceComputers(t, db, identity)
	require.Len(t, listed, 1)
	require.Equal(t, 1, listed[0].AgentCount)
	updated, err = update(added.Record.ID, domain.ServiceAudienceEmployee, domain.ServiceAudienceCustomer)
	require.NoError(t, err, "服务客户时绑定")
	require.NotNil(t, updated.Computer)
	grant = domain.ToolGrant{}
	_, err = update(added.Record.ID, domain.ServiceAudienceEmployee)
	require.Equal(t, agentaction.ValidationComputerGrantInvalid, fieldCode(err), "缺少授权时绑定的结果 = %v", err)
	grant = domain.ToolGrant{MaxLevel: domain.OperationLevelL1}
	_, err = update(added.Record.ID, domain.ServiceAudienceEmployee)
	require.Equal(t, agentaction.ValidationComputerGrantInvalid, fieldCode(err), "授权为 L1 时绑定的结果 = %v", err)
	grant = domain.ToolGrant{MaxLevel: domain.OperationLevelL0}
	_, err = update(personal.Record.ID, domain.ServiceAudienceEmployee)
	require.Equal(t, agentaction.ValidationComputerInvalid, fieldCode(err), "绑定个人电脑的结果 = %v", err)

	require.NoError(t, computeraction.NewRevokeComputerAction(db, newTestToolDecisions(db, testEnqueuer)).Execute(ctx, identity, added.Record.ID))
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, identity, agent.ID)
	require.NoError(t, err)
	require.Nil(t, detail.Computer, "撤销电脑后的 AI 员工")
	_, err = update(added.Record.ID, domain.ServiceAudienceEmployee)
	require.Equal(t, agentaction.ValidationComputerInvalid, fieldCode(err), "绑定已撤销电脑的结果 = %v", err)
}

// listWorkspaceComputers 读取工作区未撤销的工作区电脑。
func listWorkspaceComputers(t *testing.T, db *bun.DB, identity *servermodels.Identity) []computeraction.Record {
	t.Helper()
	records, err := computeraction.NewListComputersQuery(db).Execute(context.Background(), identity, domain.ComputerKindWorkspace)
	require.NoError(t, err)
	return records
}

// listComputers 读取指定身份名下未撤销的电脑。
func listComputers(t *testing.T, db *bun.DB, identity *servermodels.Identity) []computeraction.Record {
	t.Helper()
	records, err := computeraction.NewListComputersQuery(db).Execute(context.Background(), identity, domain.ComputerKindPersonal)
	require.NoError(t, err)
	return records
}
