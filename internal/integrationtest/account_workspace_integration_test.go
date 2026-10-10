//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// newAccountTestBackend 创建自托管直接后端，只接入账号与工作区入口需要的依赖。
func newAccountTestBackend(t testing.TB, db *bun.DB) *direct.Backend {
	return direct.New(db, direct.DeploymentConfig{Deployment: servertest.NewDeployment(t, db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
}

// requireSessionState 断言错误把调用方引导到指定会话入口。
func requireSessionState(t *testing.T, err error, want appservice.SessionState) {
	t.Helper()
	require.Equal(t, want, appservice.SessionStateOf(err), "error: %v", err)
}

// TestFirstInstallationAndRegistration 验证空平台进入初始化、首次安装只执行一次，以及注册与工作区创建按平台策略开放。
func TestFirstInstallationAndRegistration(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	backend := newAccountTestBackend(t, db)
	service := backend
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}

	status, err := backend.InstallationStatus(ctx, meta)
	require.NoError(t, err)
	require.False(t, status.Installed)
	require.False(t, status.RegistrationOpen)
	_, err = backend.LoadIdentity(ctx, meta)
	requireSessionState(t, err, appservice.SessionStateSetup)
	// 首次安装之前不能注册，第一个账号只能是平台管理员。
	early := appservice.RegisterInput{DisplayName: "抢先注册", Email: "early@example.test", Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai"}
	_, err = backend.Register(ctx, meta, early)
	requireSessionState(t, err, appservice.SessionStateSetup)

	install := appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: " 演示公司 ", DisplayName: "管理员", Email: "Admin@Example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	}
	installed, err := service.InstallWorkspace(ctx, meta, install)
	admin := installed.Auth
	require.NoError(t, err)
	require.NotEmpty(t, admin.Token)
	require.True(t, admin.Account.IsPlatformAdmin)
	require.Equal(t, "admin@example.test", admin.Account.Email)
	require.NotEmpty(t, installed.Workspace.ID)
	require.Equal(t, "演示公司", installed.Workspace.Name)
	require.True(t, domain.WorkspaceSlugValid(installed.Workspace.Slug), "slug %q", installed.Workspace.Slug)
	require.Equal(t, appservice.WorkspaceStatusActive, installed.Workspace.Status)
	install.Email = "second@example.test"
	_, err = service.InstallWorkspace(ctx, meta, install)
	requireSessionState(t, err, appservice.SessionStateLogin)

	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: domain.LocaleChineseSimplified}
	// 免费版本只有首个工作区，平台管理员也不能再创建。
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 1)
	require.Equal(t, installed.Workspace, workspaces.Items[0])
	require.False(t, workspaces.CanCreate)
	_, err = backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: "第二工作区"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindConflict)
	adminMeta.WorkspaceID = workspaces.Items[0].ID
	identity, err := backend.LoadIdentity(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, installed.Workspace.Slug, identity.Workspace.Slug)
	require.Equal(t, "admin@example.test", identity.User.Email)
	require.Equal(t, "管理员", identity.User.DisplayName)

	// 首次安装后默认仅限受邀注册，平台管理员开放注册后可以注册。
	register := appservice.RegisterInput{DisplayName: "成员", Email: "member@example.test", Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai"}
	_, err = backend.Register(ctx, meta, register)
	appErr, ok := errors.AsType[*appservice.Error](err)
	closed, _ := i18n.Localize("zh-CN", i18n.ErrorRegistrationClosed)
	require.True(t, ok, "closed registration error = %#v", err)
	require.Equal(t, closed, appErr.Message)
	_, err = backend.UpdatePlatformSettings(ctx, adminMeta, appservice.PlatformPoliciesInput{
		RegistrationPolicy: domain.RegistrationPolicyOpen, WorkspaceCreationPolicy: domain.WorkspaceCreationPolicyPlatformAdmin,
	})
	require.NoError(t, err)
	status, err = backend.InstallationStatus(ctx, meta)
	require.NoError(t, err)
	require.True(t, status.Installed)
	require.True(t, status.RegistrationOpen)
	member, err := service.Register(ctx, meta, register)
	require.NoError(t, err)
	require.False(t, member.Account.IsPlatformAdmin)
	_, err = backend.Register(ctx, meta, register)
	servertest.RequireFieldError(t, err, "email", i18n.FieldEmailDuplicate)
	memberMeta := appservice.RequestMeta{Token: member.Token, Locale: domain.LocaleChineseSimplified}
	// 新注册账号没有任何工作区；创建策略仅限平台管理员时不能创建工作区。
	workspaces, err = backend.ListWorkspaces(ctx, memberMeta)
	require.NoError(t, err)
	require.Empty(t, workspaces.Items)
	require.False(t, workspaces.CanCreate)
	_, err = backend.CreateWorkspace(ctx, memberMeta, appservice.WorkspaceInput{Name: "成员工作区"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)
	// 所有账号可创建时，普通账号同样受平台工作区上限约束。
	_, err = backend.UpdatePlatformSettings(ctx, adminMeta, appservice.PlatformPoliciesInput{
		RegistrationPolicy: domain.RegistrationPolicyOpen, WorkspaceCreationPolicy: domain.WorkspaceCreationPolicyAnyAccount,
	})
	require.NoError(t, err)
	_, err = backend.CreateWorkspace(ctx, memberMeta, appservice.WorkspaceInput{Name: "成员工作区"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindConflict)
	memberMeta.WorkspaceID = adminMeta.WorkspaceID
	_, err = backend.LoadIdentity(ctx, memberMeta)
	requireSessionState(t, err, appservice.SessionStateWorkspace)
}

// TestPlatformAdministration 验证平台管理接口只对平台管理员开放，并保持平台至少有一名有效平台管理员。
func TestPlatformAdministration(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	backend := newAccountTestBackend(t, db)
	service := backend
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}

	installed, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "平台管理", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	admin := installed.Auth
	require.NoError(t, err)
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: domain.LocaleChineseSimplified}
	_, err = backend.UpdatePlatformSettings(ctx, adminMeta, appservice.PlatformPoliciesInput{RegistrationPolicy: "closed", WorkspaceCreationPolicy: "everyone"})
	servertest.RequireFieldError(t, err, "registrationPolicy", i18n.FieldRegistrationPolicyInvalid)
	servertest.RequireFieldError(t, err, "workspaceCreationPolicy", i18n.FieldWorkspaceCreationPolicyInvalid)
	settings, err := backend.UpdatePlatformSettings(ctx, adminMeta, appservice.PlatformPoliciesInput{
		RegistrationPolicy: domain.RegistrationPolicyOpen, WorkspaceCreationPolicy: domain.WorkspaceCreationPolicyPlatformAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, domain.RegistrationPolicyOpen, settings.RegistrationPolicy)
	member, err := service.Register(ctx, meta, appservice.RegisterInput{
		DisplayName: "成员", Email: "member@example.test", Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	memberMeta := appservice.RequestMeta{Token: member.Token, Locale: domain.LocaleChineseSimplified}

	// 普通账号调用平台管理接口被拒绝。
	_, err = backend.GetPlatformOverview(ctx, memberMeta)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)
	_, err = backend.ListPlatformAccounts(ctx, memberMeta, appservice.PlatformAccountListInput{Status: domain.AccountStatusActive})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)

	overview, err := backend.GetPlatformOverview(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, 2, overview.AccountCount)
	require.Equal(t, 1, overview.WorkspaceCount)
	current, err := backend.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.Len(t, current.ServerID, 36)
	require.Equal(t, 1, current.Capabilities.WorkspaceLimit)
	accounts, err := service.ListPlatformAccounts(ctx, adminMeta, appservice.PlatformAccountListInput{Status: domain.AccountStatusActive})
	require.NoError(t, err)
	require.Equal(t, 2, accounts.Page.Total)
	require.Equal(t, member.Account.ID, accounts.Accounts[0].ID)
	require.Equal(t, 0, accounts.Accounts[0].WorkspaceCount)
	require.True(t, accounts.Accounts[1].IsPlatformAdmin)
	require.Equal(t, 1, accounts.Accounts[1].WorkspaceCount)
	accounts, err = backend.ListPlatformAccounts(ctx, adminMeta, appservice.PlatformAccountListInput{Query: "MEMBER@", Status: domain.AccountStatusActive})
	require.NoError(t, err)
	require.Len(t, accounts.Accounts, 1)
	require.Equal(t, member.Account.ID, accounts.Accounts[0].ID)
	workspaces, err := backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{})
	require.NoError(t, err)
	require.Len(t, workspaces.Workspaces, 1)
	require.Equal(t, installed.Workspace.Slug, workspaces.Workspaces[0].Slug)
	require.Equal(t, 1, workspaces.Workspaces[0].MemberCount)

	// 平台管理员不能修改自己的账号，平台因此始终保留操作者自己。
	_, err = backend.DeactivatePlatformAccount(ctx, adminMeta, admin.Account.ID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	_, err = backend.RevokePlatformAdmin(ctx, adminMeta, admin.Account.ID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	_, err = backend.GrantPlatformAdmin(ctx, adminMeta, "00000000-0000-0000-0000-000000000000")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)

	// 未加入工作区的账号不能设为平台管理员。
	_, err = backend.GrantPlatformAdmin(ctx, adminMeta, member.Account.ID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	servertest.AddAccountWorkspace(t, db, member.Token, "成员工作区")

	// 授予平台管理员后新管理员可以撤销原管理员，原管理员随即失去平台管理入口。
	granted, err := backend.GrantPlatformAdmin(ctx, adminMeta, member.Account.ID)
	require.NoError(t, err)
	require.True(t, granted.IsPlatformAdmin)
	loaded, err := backend.LoadAccount(ctx, memberMeta)
	require.NoError(t, err)
	require.True(t, loaded.IsPlatformAdmin)
	_, err = backend.RevokePlatformAdmin(ctx, memberMeta, admin.Account.ID)
	require.NoError(t, err)
	_, err = backend.GetPlatformOverview(ctx, adminMeta)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)
	_, err = backend.GrantPlatformAdmin(ctx, adminMeta, admin.Account.ID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)

	// 停用账号使其登录会话失效，恢复后可以重新登录。
	deactivated, err := backend.DeactivatePlatformAccount(ctx, memberMeta, admin.Account.ID)
	require.NoError(t, err)
	require.Equal(t, domain.AccountStatusInactive, deactivated.Status)
	_, err = backend.LoadAccount(ctx, adminMeta)
	requireSessionState(t, err, appservice.SessionStateLogin)
	_, err = backend.Login(ctx, meta, appservice.LoginInput{Email: "admin@example.test", Password: "password123"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	inactive, err := backend.ListPlatformAccounts(ctx, memberMeta, appservice.PlatformAccountListInput{Status: domain.AccountStatusInactive})
	require.NoError(t, err)
	require.Len(t, inactive.Accounts, 1)
	require.Equal(t, admin.Account.ID, inactive.Accounts[0].ID)
	_, err = backend.ReactivatePlatformAccount(ctx, memberMeta, admin.Account.ID)
	require.NoError(t, err)
	_, err = backend.Login(ctx, meta, appservice.LoginInput{Email: "admin@example.test", Password: "password123"})
	require.NoError(t, err, "login after reactivation")
	_, err = backend.ListPlatformAccounts(ctx, memberMeta, appservice.PlatformAccountListInput{Status: "deleted"})
	servertest.RequireFieldError(t, err, "status", i18n.FieldUserStatusInvalid)
}

// TestPlatformAdminMembershipCannotBeDeactivated 验证工作区内不能停用平台管理员的成员身份，并发停用与授予平台管理员时不会留下无成员身份的平台管理员。
func TestPlatformAdminMembershipCannotBeDeactivated(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ctx := context.Background()

	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "成员停用", DisplayName: "负责人", Email: servertest.UniqueEmail("owner"), Password: "password123"}).Identity
	memberEmail := servertest.UniqueEmail("member")
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{
		DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID, MaxServiceSessions: 10,
	})
	require.NoError(t, err)
	member := servertest.LoginMember(t, db, owner.Workspace.ID, memberEmail, "password123").Identity
	setPlatformAdmin := func(value bool) {
		t.Helper()
		_, err := db.NewUpdate().Model((*servermodels.Account)(nil)).Set("is_platform_admin = ?", value).
			Where("id = ?", member.User.AccountID).Exec(ctx)
		require.NoError(t, err)
	}

	setPlatformAdmin(true)
	_, err = testUserStatusAction(db).Execute(ctx, owner, member.User.ID, domain.IdentityStatusInactive)
	require.ErrorIs(t, err, useraction.ErrPlatformAdmin, "deactivate platform admin")
	setPlatformAdmin(false)
	_, err = db.NewUpdate().Model((*servermodels.Account)(nil)).Set("is_platform_admin = true").
		Where("id = ?", owner.User.AccountID).Exec(ctx)
	require.NoError(t, err)

	// 成员停用读取平台管理员身份后暂停，并发授予等待同一账号行，停用提交后授予因无有效成员身份失败。
	lockCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	blocker, err := db.BeginTx(lockCtx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.NewSelect().Model((*servermodels.Role)(nil)).Column("id").
		Where("workspace_id = ?", owner.Workspace.ID).Where("kind = ?", domain.RoleKindAdmin).
		For("UPDATE").Exec(lockCtx)
	require.NoError(t, err)
	deactivated := make(chan error, 1)
	go func() {
		_, err := testUserStatusAction(db).Execute(lockCtx, owner, member.User.ID, domain.IdentityStatusInactive)
		deactivated <- err
	}()
	waitChatDatabaseLock(t, lockCtx, db, `FROM "roles"`, owner.Workspace.ID)
	granted := make(chan error, 1)
	go func() {
		_, err := platformaction.NewUpdateAccountAction(db).SetPlatformAdmin(lockCtx, &servermodels.AccountIdentity{Account: owner.Account}, member.User.AccountID, true)
		granted <- err
	}()
	waitChatDatabaseLock(t, lockCtx, db, `FROM "accounts"`, member.User.AccountID)
	require.NoError(t, blocker.Commit())
	require.NoError(t, waitChatResult(t, lockCtx, deactivated), "deactivate member")
	require.ErrorIs(t, waitChatResult(t, lockCtx, granted), platformaction.ErrNoActiveMembership, "grant platform admin")
}

// TestAccountWorkspacesAreIsolated 验证账号加入多个工作区后可分别进入，且无法进入不属于自己的工作区。
func TestAccountWorkspacesAreIsolated(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	backend := newAccountTestBackend(t, db)
	ctx := context.Background()

	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "第一工作区", DisplayName: "负责人", Email: servertest.UniqueEmail("owner"), Password: "password123"})
	outsider := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "外部工作区", DisplayName: "外部成员", Email: servertest.UniqueEmail("outsider"), Password: "password123"})
	ownerMeta := appservice.RequestMeta{Token: owner.Token, Locale: domain.LocaleChineseSimplified}

	// 在第一个工作区修改的姓名作为新工作区的默认成员姓名。
	firstMeta := ownerMeta
	firstMeta.WorkspaceID = owner.Identity.Workspace.ID
	_, err = backend.UpdateProfile(ctx, firstMeta, appservice.ProfileInput{DisplayName: "改名后的负责人", Email: owner.Identity.Account.Email})
	require.NoError(t, err)
	second := servertest.AddAccountWorkspace(t, db, owner.Token, "第二工作区").Workspace
	secondMeta := ownerMeta
	secondMeta.WorkspaceID = second.ID
	secondIdentity, err := backend.LoadIdentity(ctx, secondMeta)
	require.NoError(t, err)
	require.Equal(t, "改名后的负责人", secondIdentity.User.DisplayName)
	_, err = backend.CreateWorkspace(ctx, ownerMeta, appservice.WorkspaceInput{Name: ""})
	servertest.RequireFieldError(t, err, "name", i18n.FieldWorkspaceNameRequired)

	workspaces, err := backend.ListWorkspaces(ctx, ownerMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 2)
	// 同一个会话按请求目标工作区解析出各自的成员身份。
	for _, workspace := range workspaces.Items {
		meta := ownerMeta
		meta.WorkspaceID = workspace.ID
		identity, err := backend.LoadIdentity(ctx, meta)
		require.NoError(t, err)
		require.Equal(t, workspace.ID, identity.Workspace.ID, "identity in %s", workspace.Slug)
	}

	_, err = backend.LoadIdentity(ctx, ownerMeta)
	requireSessionState(t, err, appservice.SessionStateWorkspace)
	outsiderMeta := appservice.RequestMeta{Token: outsider.Token, WorkspaceID: second.ID, Locale: domain.LocaleChineseSimplified}
	_, err = backend.LoadIdentity(ctx, outsiderMeta)
	requireSessionState(t, err, appservice.SessionStateWorkspace)
	_, err = backend.ListTeams(ctx, outsiderMeta, appservice.TeamListInput{})
	requireSessionState(t, err, appservice.SessionStateWorkspace)

	// 退出登录后该会话在所有工作区都失效。
	require.NoError(t, backend.Logout(ctx, ownerMeta))
	for _, workspace := range workspaces.Items {
		meta := ownerMeta
		meta.WorkspaceID = workspace.ID
		_, err := backend.LoadIdentity(ctx, meta)
		requireSessionState(t, err, appservice.SessionStateLogin)
	}
}

// TestChangePasswordRevokesOtherSessions 验证修改密码后同一账号的其他登录会话失效，当前会话保留。
func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	backend := newAccountTestBackend(t, db)
	ctx := context.Background()

	email := servertest.UniqueEmail("password")
	current := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "密码工作区", DisplayName: "成员", Email: email, Password: "password123"})
	other := servertest.LoginMember(t, db, current.Identity.Workspace.ID, email, "password123")
	currentMeta := appservice.RequestMeta{Token: current.Token, WorkspaceID: current.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	otherMeta := appservice.RequestMeta{Token: other.Token, WorkspaceID: current.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}

	err = backend.ChangePassword(ctx, currentMeta, appservice.ChangePasswordInput{CurrentPassword: "wrong-password", NewPassword: "password456"})
	servertest.RequireFieldError(t, err, "currentPassword", i18n.FieldCurrentPasswordIncorrect)
	require.NoError(t, backend.ChangePassword(ctx, currentMeta, appservice.ChangePasswordInput{CurrentPassword: "password123", NewPassword: "password456"}))
	_, err = backend.LoadIdentity(ctx, currentMeta)
	require.NoError(t, err, "current session revoked")
	_, err = backend.LoadIdentity(ctx, otherMeta)
	requireSessionState(t, err, appservice.SessionStateLogin)
	_, err = backend.Login(ctx, appservice.RequestMeta{}, appservice.LoginInput{Email: email, Password: "password123"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	servertest.LoginMember(t, db, current.Identity.Workspace.ID, email, "password456")
}
