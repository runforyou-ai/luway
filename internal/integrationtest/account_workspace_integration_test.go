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
	return direct.New(db, direct.DeploymentConfig{Deployment: servertest.NewDeployment(t, db)}, nil, nil, testEnqueuer, nil, nil, nil)
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

	install := appservice.InstallPlatformInput{
		PublicURL: servertest.PublicURL, WorkspaceName: " 演示公司 ", DisplayName: "管理员", Email: "Admin@Example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	}
	installed, err := service.InstallPlatform(ctx, meta, install)
	require.NoError(t, err)
	require.NotEmpty(t, installed.Token)
	require.True(t, installed.Account.IsPlatformAdmin)
	require.Equal(t, "admin@example.test", installed.Account.Email)
	install.Email = "second@example.test"
	_, err = service.InstallPlatform(ctx, meta, install)
	requireSessionState(t, err, appservice.SessionStateLogin)

	adminMeta := servertest.AdminMeta(installed.Token)
	userAuth, err := backend.Login(ctx, meta, appservice.LoginInput{Email: "admin@example.test", Password: "password123"})
	require.NoError(t, err)
	require.False(t, userAuth.ExpiresAt.IsZero())
	userMeta := appservice.RequestMeta{Token: userAuth.Token, Locale: domain.LocaleChineseSimplified}
	// 首次安装创建默认工作区；未授权时自建额度 0，不能再创建。
	workspaces, err := backend.ListWorkspaces(ctx, userMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 1)
	created := workspaces.Items[0]
	require.Equal(t, "演示公司", created.Name)
	require.True(t, domain.WorkspaceSlugValid(created.Slug), "slug %q", created.Slug)
	require.Equal(t, appservice.WorkspaceStatusActive, created.Status)
	require.False(t, workspaces.CanCreate)
	_, err = backend.CreateWorkspace(ctx, userMeta, appservice.WorkspaceInput{Name: "第二工作区"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)
	userMeta.WorkspaceID = created.ID
	identity, err := backend.LoadIdentity(ctx, userMeta)
	require.NoError(t, err)
	require.Equal(t, created.Slug, identity.Workspace.Slug)
	require.Equal(t, "admin@example.test", identity.User.Email)
	require.Equal(t, "管理员", identity.User.DisplayName)

	// 首次安装后默认仅限受邀注册，平台管理员开放注册后可以注册；额度 0 时进入默认工作区。
	register := appservice.RegisterInput{DisplayName: "成员", Email: "member@example.test", Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai"}
	_, err = backend.Register(ctx, meta, register)
	appErr, ok := errors.AsType[*appservice.Error](err)
	closed, _ := i18n.Localize("zh-CN", i18n.ErrorRegistrationClosed)
	require.True(t, ok, "closed registration error = %#v", err)
	require.Equal(t, closed, appErr.Message)
	input := loadPlatformSiteInput(t, backend, ctx, adminMeta)
	input.RegistrationPolicy = domain.RegistrationPolicyOpen
	input.WorkspaceCreationQuota = 0
	_, err = backend.UpdatePlatformSite(ctx, adminMeta, input)
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
	workspaces, err = backend.ListWorkspaces(ctx, memberMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 1)
	require.Equal(t, created.ID, workspaces.Items[0].ID)
	require.False(t, workspaces.CanCreate)
	_, err = backend.CreateWorkspace(ctx, memberMeta, appservice.WorkspaceInput{Name: "成员工作区"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)

	// 已授权且额度 ≥1 时，自助注册账号可自建工作区；未授权时即使提交额度也会锁 0。
	input = loadPlatformSiteInput(t, backend, ctx, adminMeta)
	input.RegistrationPolicy = domain.RegistrationPolicyOpen
	input.WorkspaceCreationQuota = 2
	settings, err := backend.UpdatePlatformSite(ctx, adminMeta, input)
	require.NoError(t, err)
	require.Equal(t, 0, settings.WorkspaceCreationQuota)
	servertest.SeedActiveLicense(t, db)
	input = loadPlatformSiteInput(t, backend, ctx, adminMeta)
	input.RegistrationPolicy = domain.RegistrationPolicyOpen
	input.WorkspaceCreationQuota = 1
	settings, err = backend.UpdatePlatformSite(ctx, adminMeta, input)
	require.NoError(t, err)
	require.Equal(t, 1, settings.WorkspaceCreationQuota)
	input = loadPlatformSiteInput(t, backend, ctx, adminMeta)
	input.RegistrationPolicy = domain.RegistrationPolicyOpen
	input.WorkspaceCreationQuota = 0
	_, err = backend.UpdatePlatformSite(ctx, adminMeta, input)
	servertest.RequireFieldError(t, err, "workspaceCreationQuota", i18n.FieldWorkspaceCreationQuotaDecrease)
	input = loadPlatformSiteInput(t, backend, ctx, adminMeta)
	input.RegistrationPolicy = domain.RegistrationPolicyOpen
	input.WorkspaceCreationQuota = 2
	settings, err = backend.UpdatePlatformSite(ctx, adminMeta, input)
	require.NoError(t, err)
	require.Equal(t, 2, settings.WorkspaceCreationQuota)
	input = loadPlatformSiteInput(t, backend, ctx, adminMeta)
	input.RegistrationPolicy = domain.RegistrationPolicyOpen
	input.WorkspaceCreationQuota = 1
	_, err = backend.UpdatePlatformSite(ctx, adminMeta, input)
	servertest.RequireFieldError(t, err, "workspaceCreationQuota", i18n.FieldWorkspaceCreationQuotaDecrease)
	settings, err = backend.GetPlatformSite(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, 2, settings.WorkspaceCreationQuota)
	selfServe, err := service.Register(ctx, meta, appservice.RegisterInput{
		DisplayName: "自建", Email: "self@example.test", Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	selfMeta := appservice.RequestMeta{Token: selfServe.Token, Locale: domain.LocaleChineseSimplified}
	workspaces, err = backend.ListWorkspaces(ctx, selfMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 1)
	require.Equal(t, "自建", workspaces.Items[0].Name)
	require.True(t, workspaces.CanCreate)
	_, err = backend.CreateWorkspace(ctx, selfMeta, appservice.WorkspaceInput{Name: "第二个工作区"})
	require.NoError(t, err)
	workspaces, err = backend.ListWorkspaces(ctx, selfMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 2)
	require.False(t, workspaces.CanCreate)
	_, err = backend.CreateWorkspace(ctx, selfMeta, appservice.WorkspaceInput{Name: "超额工作区"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindConflict)
	memberMeta.WorkspaceID = created.ID
	_, err = backend.LoadIdentity(ctx, memberMeta)
	require.NoError(t, err)
}

// TestPlatformAdministration 验证平台管理接口只对平台管理员开放，并保持平台至少有一名有效平台管理员。
func TestPlatformAdministration(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	backend := newAccountTestBackend(t, db)
	service := backend
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}

	installed, err := service.InstallPlatform(ctx, meta, appservice.InstallPlatformInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "平台管理", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	admin := installed.Account
	adminMeta := servertest.AdminMeta(installed.Token)
	userAuth, err := backend.Login(ctx, meta, appservice.LoginInput{Email: "admin@example.test", Password: "password123"})
	require.NoError(t, err)
	userMeta := appservice.RequestMeta{Token: userAuth.Token, Locale: domain.LocaleChineseSimplified}
	workspaces, err := backend.ListWorkspaces(ctx, userMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 1)
	workspace := workspaces.Items[0]
	input := loadPlatformSiteInput(t, backend, ctx, adminMeta)
	input.RegistrationPolicy = "closed"
	_, err = backend.UpdatePlatformSite(ctx, adminMeta, input)
	servertest.RequireFieldError(t, err, "registrationPolicy", i18n.FieldRegistrationPolicyInvalid)
	input = loadPlatformSiteInput(t, backend, ctx, adminMeta)
	input.RegistrationPolicy = domain.RegistrationPolicyOpen
	input.WorkspaceCreationQuota = 0
	settings, err := backend.UpdatePlatformSite(ctx, adminMeta, input)
	require.NoError(t, err)
	require.Equal(t, domain.RegistrationPolicyOpen, settings.RegistrationPolicy)
	member, err := service.Register(ctx, meta, appservice.RegisterInput{
		DisplayName: "成员", Email: "member@example.test", Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	memberMeta := appservice.RequestMeta{Token: member.Token, Locale: domain.LocaleChineseSimplified}

	// 普通账号没有后台会话，不能调用平台管理接口。
	_, err = backend.GetPlatformOverview(ctx, memberMeta)
	requireSessionState(t, err, appservice.SessionStateLogin)
	_, err = backend.ListPlatformAccounts(ctx, memberMeta, appservice.PlatformAccountListInput{Status: domain.AccountStatusActive})
	requireSessionState(t, err, appservice.SessionStateLogin)

	overview, err := backend.GetPlatformOverview(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, 2, overview.AccountCount)
	require.Equal(t, 1, overview.WorkspaceCount)
	current, err := backend.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.Len(t, current.DeploymentID, 36)
	require.Equal(t, domain.LicenseStatusNone, current.Status)
	require.False(t, current.Licensed)
	accounts, err := service.ListPlatformAccounts(ctx, adminMeta, appservice.PlatformAccountListInput{Status: domain.AccountStatusActive})
	require.NoError(t, err)
	require.Equal(t, 2, accounts.Page.Total)
	require.Equal(t, member.Account.ID, accounts.Accounts[0].ID)
	require.Equal(t, 1, accounts.Accounts[0].WorkspaceCount)
	require.True(t, accounts.Accounts[1].IsPlatformAdmin)
	require.Equal(t, 1, accounts.Accounts[1].WorkspaceCount)
	accounts, err = backend.ListPlatformAccounts(ctx, adminMeta, appservice.PlatformAccountListInput{Query: "MEMBER@", Status: domain.AccountStatusActive})
	require.NoError(t, err)
	require.Len(t, accounts.Accounts, 1)
	require.Equal(t, member.Account.ID, accounts.Accounts[0].ID)
	platformWorkspaces, err := backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{})
	require.NoError(t, err)
	require.Len(t, platformWorkspaces.Workspaces, 1)
	require.Equal(t, workspace.Slug, platformWorkspaces.Workspaces[0].Slug)
	require.Equal(t, 2, platformWorkspaces.Workspaces[0].MemberCount)

	// 工作区筛选只返回有效成员账号，并与关键词和分页总数一致。
	accounts, err = service.ListPlatformAccounts(ctx, adminMeta, appservice.PlatformAccountListInput{WorkspaceID: workspace.ID, Status: domain.AccountStatusActive, Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.Equal(t, 2, accounts.Page.Total)
	require.Len(t, accounts.Accounts, 1)
	accounts, err = backend.ListPlatformAccounts(ctx, adminMeta, appservice.PlatformAccountListInput{WorkspaceID: workspace.ID, Query: "MEMBER@", Status: domain.AccountStatusActive})
	require.NoError(t, err)
	require.Len(t, accounts.Accounts, 1)
	require.Equal(t, member.Account.ID, accounts.Accounts[0].ID)
	_, err = service.ListPlatformAccounts(ctx, adminMeta, appservice.PlatformAccountListInput{WorkspaceID: "invalid", Status: domain.AccountStatusActive})
	servertest.RequireFieldError(t, err, "workspaceId", i18n.ErrorPlatformWorkspaceNotFound)

	// 平台管理员不能修改自己的账号，平台因此始终保留操作者自己。
	_, err = backend.DeactivatePlatformAccount(ctx, adminMeta, admin.ID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	_, err = backend.RevokePlatformAdmin(ctx, adminMeta, admin.ID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	_, err = backend.GrantPlatformAdmin(ctx, adminMeta, "00000000-0000-0000-0000-000000000000")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)

	// 未加入工作区的账号也可以设为平台管理员。
	granted, err := backend.GrantPlatformAdmin(ctx, adminMeta, member.Account.ID)
	require.NoError(t, err)
	require.True(t, granted.IsPlatformAdmin)
	loaded, err := backend.LoadAccount(ctx, memberMeta)
	require.NoError(t, err)
	require.True(t, loaded.IsPlatformAdmin)
	memberAdmin, err := backend.AdminLogin(ctx, meta, appservice.LoginInput{Email: "member@example.test", Password: "password123"})
	require.NoError(t, err)
	memberAdminMeta := servertest.AdminMeta(memberAdmin.Token)
	_, err = backend.RevokePlatformAdmin(ctx, memberAdminMeta, admin.ID)
	require.NoError(t, err)
	_, err = backend.GetPlatformOverview(ctx, adminMeta)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)
	_, err = backend.GrantPlatformAdmin(ctx, adminMeta, admin.ID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)

	// 停用账号使其登录会话失效，恢复后可以重新登录。
	deactivated, err := backend.DeactivatePlatformAccount(ctx, memberAdminMeta, admin.ID)
	require.NoError(t, err)
	require.Equal(t, domain.AccountStatusInactive, deactivated.Status)
	_, err = backend.LoadAccount(ctx, userMeta)
	requireSessionState(t, err, appservice.SessionStateLogin)
	_, err = backend.Login(ctx, meta, appservice.LoginInput{Email: "admin@example.test", Password: "password123"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	inactive, err := backend.ListPlatformAccounts(ctx, memberAdminMeta, appservice.PlatformAccountListInput{Status: domain.AccountStatusInactive})
	require.NoError(t, err)
	require.Len(t, inactive.Accounts, 1)
	require.Equal(t, admin.ID, inactive.Accounts[0].ID)
	_, err = backend.ReactivatePlatformAccount(ctx, memberAdminMeta, admin.ID)
	require.NoError(t, err)
	_, err = backend.Login(ctx, meta, appservice.LoginInput{Email: "admin@example.test", Password: "password123"})
	require.NoError(t, err, "login after reactivation")
	_, err = backend.ListPlatformAccounts(ctx, memberAdminMeta, appservice.PlatformAccountListInput{Status: "deleted"})
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

	// 成员停用与授予平台管理员并发时，授予不再要求有效成员身份，停用提交后授予仍可成功。
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
	require.NoError(t, waitChatResult(t, lockCtx, granted), "grant platform admin without membership")
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

// TestChangeAdminPassword 验证平台后台修改密码接口可更新管理员密码。
func TestChangeAdminPassword(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	backend := newAccountTestBackend(t, db)
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}

	installed := servertest.InstallPlatform(t, backend, appservice.InstallPlatformInput{
		PublicURL: servertest.PublicURL, DisplayName: "管理员", Email: "admin-password@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	adminMeta := servertest.AdminMeta(installed.AdminToken)

	err := backend.ChangeAdminPassword(ctx, adminMeta, appservice.ChangePasswordInput{CurrentPassword: "wrong-password", NewPassword: "password456"})
	servertest.RequireFieldError(t, err, "currentPassword", i18n.FieldCurrentPasswordIncorrect)
	require.NoError(t, backend.ChangeAdminPassword(ctx, adminMeta, appservice.ChangePasswordInput{CurrentPassword: "password123", NewPassword: "password456"}))
	_, err = backend.LoadAdminAccount(ctx, adminMeta)
	require.NoError(t, err, "current admin session should remain")
	_, err = backend.AdminLogin(ctx, meta, appservice.LoginInput{Email: "admin-password@example.test", Password: "password123"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	_, err = backend.AdminLogin(ctx, meta, appservice.LoginInput{Email: "admin-password@example.test", Password: "password456"})
	require.NoError(t, err)
}
