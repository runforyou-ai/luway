//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// openEmptyDatabase 在测试库所在实例上新建并迁移一个空数据库，测试结束后删除；用于依赖部署尚无账号的首次安装场景。
func openEmptyDatabase(t *testing.T) *bun.DB {
	t.Helper()
	ctx := context.Background()
	config := servertest.DatabaseConfig(t)
	base, err := serverstorage.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	name := config.Name + "_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:16]
	if _, err := base.DB().ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	config.Name = name
	store, err := serverstorage.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_, _ = base.DB().ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return store.DB()
}

// newAccountTestBackend 创建自托管直接后端，只接入账号与工作区入口需要的依赖。
func newAccountTestBackend(db *bun.DB) *direct.Backend {
	return direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL}, nil, serverfilecontent.S3Config{}, nil, nil, newTestTasks(db), nil, nil, nil)
}

// requireSessionState 断言错误把调用方引导到指定会话入口。
func requireSessionState(t *testing.T, err error, want appservice.SessionState) {
	t.Helper()
	if state := appservice.SessionStateOf(err); state != want {
		t.Fatalf("session state = %q, want %q (error: %v)", state, want, err)
	}
}

// requireErrorKind 断言错误是指定种类的业务错误。
func requireErrorKind(t *testing.T, err error, want appservice.ErrorKind) {
	t.Helper()
	if appErr, ok := errors.AsType[*appservice.Error](err); !ok || appErr.Kind != want {
		t.Fatalf("error = %#v, want kind %q", err, want)
	}
}

// requireFieldError 断言错误是指定字段的本地化校验失败。
func requireFieldError(t *testing.T, err error, field string, key i18n.Key) {
	t.Helper()
	appErr, ok := errors.AsType[*appservice.Error](err)
	want, _ := i18n.Localize("zh-CN", key)
	if !ok || appErr.Fields[field] != want {
		t.Fatalf("error = %#v, want field %s = %q", err, field, want)
	}
}

// TestFirstInstallationAndRegistration 验证空部署进入初始化、首次安装只执行一次，以及注册与工作区创建按部署策略开放。
func TestFirstInstallationAndRegistration(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	backend := newAccountTestBackend(db)
	service := appservice.New(backend)
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}

	status, err := backend.InstallationStatus(ctx, meta)
	if err != nil || status.Installed || status.RegistrationOpen {
		t.Fatalf("empty status = %#v, err = %v", status, err)
	}
	_, err = backend.LoadIdentity(ctx, meta)
	requireSessionState(t, err, appservice.SessionStateSetup)
	// 首次安装之前不能注册，第一个账号只能是部署管理员。
	early := appservice.RegisterInput{DisplayName: "抢先注册", Email: "early@example.test", Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai"}
	_, err = backend.Register(ctx, meta, early)
	requireSessionState(t, err, appservice.SessionStateSetup)

	install := appservice.InstallWorkspaceInput{
		WorkspaceName: "演示公司", WorkspaceSlug: " Demo-Team ", DisplayName: "管理员", Email: "Admin@Example.test",
		Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	}
	admin, err := service.InstallWorkspace(ctx, meta, install)
	if err != nil || admin.Token == "" || !admin.Account.IsDeploymentAdmin || admin.Account.Email != "admin@example.test" {
		t.Fatalf("install auth = %#v, err = %v", admin, err)
	}
	install.Email, install.WorkspaceSlug = "second@example.test", "second"
	_, err = service.InstallWorkspace(ctx, meta, install)
	requireSessionState(t, err, appservice.SessionStateLogin)

	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: appservice.LocaleChineseSimplified}
	// 免费实例只有首个工作区，部署管理员也不能再创建。
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	if err != nil || len(workspaces.Items) != 1 || workspaces.Items[0].Slug != "demo-team" || workspaces.CanCreate {
		t.Fatalf("workspaces = %#v, err = %v", workspaces, err)
	}
	_, err = backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: "第二工作区", Slug: "second-team"})
	requireErrorKind(t, err, appservice.ErrorKindConflict)
	adminMeta.WorkspaceID = workspaces.Items[0].ID
	identity, err := backend.LoadIdentity(ctx, adminMeta)
	if err != nil || identity.Organization.Slug != "demo-team" || identity.User.Email != "admin@example.test" || identity.User.DisplayName != "管理员" {
		t.Fatalf("identity = %#v, err = %v", identity, err)
	}

	// 首次安装后默认仅限受邀注册，部署管理员开放注册后可以注册。
	register := appservice.RegisterInput{DisplayName: "成员", Email: "member@example.test", Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai"}
	_, err = backend.Register(ctx, meta, register)
	appErr, ok := errors.AsType[*appservice.Error](err)
	if closed, _ := i18n.Localize("zh-CN", i18n.ErrorRegistrationClosed); !ok || appErr.Message != closed {
		t.Fatalf("closed registration error = %#v", err)
	}
	if _, err := backend.UpdateDeploymentSettings(ctx, adminMeta, appservice.DeploymentPoliciesInput{
		RegistrationPolicy: appservice.RegistrationPolicyOpen, WorkspaceCreationPolicy: appservice.WorkspaceCreationPolicyDeploymentAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	status, err = backend.InstallationStatus(ctx, meta)
	if err != nil || !status.Installed || !status.RegistrationOpen {
		t.Fatalf("installed status = %#v, err = %v", status, err)
	}
	member, err := service.Register(ctx, meta, register)
	if err != nil || member.Account.IsDeploymentAdmin {
		t.Fatalf("register auth = %#v, err = %v", member, err)
	}
	_, err = backend.Register(ctx, meta, register)
	requireFieldError(t, err, "email", i18n.FieldEmailDuplicate)
	memberMeta := appservice.RequestMeta{Token: member.Token, Locale: appservice.LocaleChineseSimplified}
	// 新注册账号没有任何工作区；创建策略仅限部署管理员时不能创建工作区。
	workspaces, err = backend.ListWorkspaces(ctx, memberMeta)
	if err != nil || len(workspaces.Items) != 0 || workspaces.CanCreate {
		t.Fatalf("member workspaces = %#v, err = %v", workspaces, err)
	}
	_, err = backend.CreateWorkspace(ctx, memberMeta, appservice.WorkspaceInput{Name: "成员工作区", Slug: "member-team"})
	requireErrorKind(t, err, appservice.ErrorKindForbidden)
	// 所有账号可创建时，普通账号同样受实例工作区上限约束。
	if _, err := backend.UpdateDeploymentSettings(ctx, adminMeta, appservice.DeploymentPoliciesInput{
		RegistrationPolicy: appservice.RegistrationPolicyOpen, WorkspaceCreationPolicy: appservice.WorkspaceCreationPolicyAnyAccount,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = backend.CreateWorkspace(ctx, memberMeta, appservice.WorkspaceInput{Name: "成员工作区", Slug: "member-team"})
	requireErrorKind(t, err, appservice.ErrorKindConflict)
	memberMeta.WorkspaceID = adminMeta.WorkspaceID
	_, err = backend.LoadIdentity(ctx, memberMeta)
	requireSessionState(t, err, appservice.SessionStateWorkspace)
}

// TestDeploymentAdministration 验证部署管理接口只对部署管理员开放，并保持部署至少有一名有效部署管理员。
func TestDeploymentAdministration(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	backend := newAccountTestBackend(db)
	service := appservice.New(backend)
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}

	admin, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		WorkspaceName: "部署管理", WorkspaceSlug: "deployment-admin", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: appservice.LocaleChineseSimplified}
	_, err = backend.UpdateDeploymentSettings(ctx, adminMeta, appservice.DeploymentPoliciesInput{RegistrationPolicy: "closed", WorkspaceCreationPolicy: "everyone"})
	requireFieldError(t, err, "registrationPolicy", i18n.FieldRegistrationPolicyInvalid)
	requireFieldError(t, err, "workspaceCreationPolicy", i18n.FieldWorkspaceCreationPolicyInvalid)
	settings, err := backend.UpdateDeploymentSettings(ctx, adminMeta, appservice.DeploymentPoliciesInput{
		RegistrationPolicy: appservice.RegistrationPolicyOpen, WorkspaceCreationPolicy: appservice.WorkspaceCreationPolicyDeploymentAdmin,
	})
	if err != nil || settings.RegistrationPolicy != appservice.RegistrationPolicyOpen {
		t.Fatalf("settings = %#v, err = %v", settings, err)
	}
	member, err := service.Register(ctx, meta, appservice.RegisterInput{
		DisplayName: "成员", Email: "member@example.test", Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	memberMeta := appservice.RequestMeta{Token: member.Token, Locale: appservice.LocaleChineseSimplified}

	// 普通账号调用部署管理接口被拒绝。
	_, err = backend.GetDeploymentOverview(ctx, memberMeta)
	requireErrorKind(t, err, appservice.ErrorKindForbidden)
	_, err = backend.ListDeploymentAccounts(ctx, memberMeta, appservice.DeploymentAccountListInput{Status: appservice.AccountStatusActive})
	requireErrorKind(t, err, appservice.ErrorKindForbidden)

	overview, err := backend.GetDeploymentOverview(ctx, adminMeta)
	if err != nil || len(overview.InstanceID) != 36 || overview.AccountCount != 2 || overview.WorkspaceCount != 1 || overview.Capabilities.WorkspaceLimit != 1 {
		t.Fatalf("overview = %#v, err = %v", overview, err)
	}
	accounts, err := service.ListDeploymentAccounts(ctx, adminMeta, appservice.DeploymentAccountListInput{Status: appservice.AccountStatusActive})
	if err != nil || accounts.Page.Total != 2 || accounts.Accounts[0].ID != member.Account.ID || accounts.Accounts[0].WorkspaceCount != 0 ||
		!accounts.Accounts[1].IsDeploymentAdmin || accounts.Accounts[1].WorkspaceCount != 1 {
		t.Fatalf("accounts = %#v, err = %v", accounts, err)
	}
	accounts, err = backend.ListDeploymentAccounts(ctx, adminMeta, appservice.DeploymentAccountListInput{Query: "MEMBER@", Status: appservice.AccountStatusActive})
	if err != nil || len(accounts.Accounts) != 1 || accounts.Accounts[0].ID != member.Account.ID {
		t.Fatalf("searched accounts = %#v, err = %v", accounts, err)
	}
	workspaces, err := backend.ListDeploymentWorkspaces(ctx, adminMeta, appservice.DeploymentWorkspaceListInput{})
	if err != nil || len(workspaces.Workspaces) != 1 || workspaces.Workspaces[0].Slug != "deployment-admin" || workspaces.Workspaces[0].MemberCount != 1 {
		t.Fatalf("workspaces = %#v, err = %v", workspaces, err)
	}

	// 部署管理员不能修改自己的账号，部署因此始终保留操作者自己。
	_, err = backend.DeactivateDeploymentAccount(ctx, adminMeta, admin.Account.ID)
	requireErrorKind(t, err, appservice.ErrorKindInvalid)
	_, err = backend.RevokeDeploymentAdmin(ctx, adminMeta, admin.Account.ID)
	requireErrorKind(t, err, appservice.ErrorKindInvalid)
	_, err = backend.GrantDeploymentAdmin(ctx, adminMeta, "00000000-0000-0000-0000-000000000000")
	requireErrorKind(t, err, appservice.ErrorKindNotFound)

	// 未加入工作区的账号不能设为部署管理员。
	_, err = backend.GrantDeploymentAdmin(ctx, adminMeta, member.Account.ID)
	requireErrorKind(t, err, appservice.ErrorKindInvalid)
	addAccountWorkspace(t, db, member.Token, "成员工作区")

	// 授予部署管理员后新管理员可以撤销原管理员，原管理员随即失去部署管理入口。
	granted, err := backend.GrantDeploymentAdmin(ctx, adminMeta, member.Account.ID)
	if err != nil || !granted.IsDeploymentAdmin {
		t.Fatalf("granted = %#v, err = %v", granted, err)
	}
	if loaded, err := backend.LoadAccount(ctx, memberMeta); err != nil || !loaded.IsDeploymentAdmin {
		t.Fatalf("member account = %#v, err = %v", loaded, err)
	}
	if _, err := backend.RevokeDeploymentAdmin(ctx, memberMeta, admin.Account.ID); err != nil {
		t.Fatal(err)
	}
	_, err = backend.GetDeploymentOverview(ctx, adminMeta)
	requireErrorKind(t, err, appservice.ErrorKindForbidden)
	_, err = backend.GrantDeploymentAdmin(ctx, adminMeta, admin.Account.ID)
	requireErrorKind(t, err, appservice.ErrorKindForbidden)

	// 停用账号使其登录会话失效，恢复后可以重新登录。
	deactivated, err := backend.DeactivateDeploymentAccount(ctx, memberMeta, admin.Account.ID)
	if err != nil || deactivated.Status != appservice.AccountStatusInactive {
		t.Fatalf("deactivated = %#v, err = %v", deactivated, err)
	}
	_, err = backend.LoadAccount(ctx, adminMeta)
	requireSessionState(t, err, appservice.SessionStateLogin)
	_, err = backend.Login(ctx, meta, appservice.LoginInput{Email: "admin@example.test", Password: "password123"})
	requireErrorKind(t, err, appservice.ErrorKindInvalid)
	inactive, err := backend.ListDeploymentAccounts(ctx, memberMeta, appservice.DeploymentAccountListInput{Status: appservice.AccountStatusInactive})
	if err != nil || len(inactive.Accounts) != 1 || inactive.Accounts[0].ID != admin.Account.ID {
		t.Fatalf("inactive accounts = %#v, err = %v", inactive, err)
	}
	if _, err := backend.ReactivateDeploymentAccount(ctx, memberMeta, admin.Account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Login(ctx, meta, appservice.LoginInput{Email: "admin@example.test", Password: "password123"}); err != nil {
		t.Fatalf("login after reactivation: %v", err)
	}
	_, err = backend.ListDeploymentAccounts(ctx, memberMeta, appservice.DeploymentAccountListInput{Status: "deleted"})
	requireFieldError(t, err, "status", i18n.FieldUserStatusInvalid)
}

// TestDeploymentAdminMembershipCannotBeDeactivated 验证工作区内不能停用部署管理员的成员身份，并发停用与授予部署管理员时不会留下无成员身份的部署管理员。
func TestDeploymentAdminMembershipCannotBeDeactivated(t *testing.T) {
	t.Parallel()
	store, err := serverstorage.Open(context.Background(), servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ctx := context.Background()

	owner := installWorkspace(t, db, workspaceSpec{Name: "成员停用", DisplayName: "负责人", Email: uniqueEmail("owner"), Password: "password123"}).Identity
	memberEmail := uniqueEmail("member")
	if _, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{
		DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID, MaxServiceSessions: 10,
	}); err != nil {
		t.Fatal(err)
	}
	member := loginMember(t, db, owner.Organization.ID, memberEmail, "password123").Identity
	setDeploymentAdmin := func(value bool) {
		t.Helper()
		if _, err := db.NewUpdate().Model((*servermodels.Account)(nil)).Set("is_deployment_admin = ?", value).
			Where("id = ?", member.User.AccountID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	setDeploymentAdmin(true)
	if _, err := testUserStatusAction(db).Execute(ctx, owner, member.User.ID, domain.IdentityStatusInactive); !errors.Is(err, useraction.ErrDeploymentAdmin) {
		t.Fatalf("deactivate deployment admin error = %v", err)
	}
	setDeploymentAdmin(false)
	if _, err := db.NewUpdate().Model((*servermodels.Account)(nil)).Set("is_deployment_admin = true").
		Where("id = ?", owner.User.AccountID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	// 成员停用读取部署管理员身份后暂停，并发授予等待同一账号行，停用提交后授予因无有效成员身份失败。
	lockCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	blocker, err := db.BeginTx(lockCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err := blocker.NewSelect().Model((*servermodels.Role)(nil)).Column("id").
		Where("organization_id = ?", owner.Organization.ID).Where("kind = ?", domain.RoleKindAdmin).
		For("UPDATE").Exec(lockCtx); err != nil {
		t.Fatal(err)
	}
	deactivated := make(chan error, 1)
	go func() {
		_, err := testUserStatusAction(db).Execute(lockCtx, owner, member.User.ID, domain.IdentityStatusInactive)
		deactivated <- err
	}()
	waitChatDatabaseLock(t, lockCtx, db, `FROM "roles"`, owner.Organization.ID)
	granted := make(chan error, 1)
	go func() {
		_, err := deploymentaction.NewUpdateAccountAction(db).SetDeploymentAdmin(lockCtx, &servermodels.AccountIdentity{Account: owner.Account}, member.User.AccountID, true)
		granted <- err
	}()
	waitChatDatabaseLock(t, lockCtx, db, `FROM "accounts"`, member.User.AccountID)
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := waitChatResult(t, lockCtx, deactivated); err != nil {
		t.Fatalf("deactivate member: %v", err)
	}
	if err := waitChatResult(t, lockCtx, granted); !errors.Is(err, deploymentaction.ErrNoActiveMembership) {
		t.Fatalf("grant deployment admin error = %v", err)
	}
}

// TestAccountWorkspacesAreIsolated 验证账号加入多个工作区后可分别进入，且无法进入不属于自己的工作区。
func TestAccountWorkspacesAreIsolated(t *testing.T) {
	t.Parallel()
	store, err := serverstorage.Open(context.Background(), servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	backend := newAccountTestBackend(db)
	ctx := context.Background()

	owner := installWorkspace(t, db, workspaceSpec{Name: "第一工作区", DisplayName: "负责人", Email: uniqueEmail("owner"), Password: "password123"})
	outsider := installWorkspace(t, db, workspaceSpec{Name: "外部工作区", DisplayName: "外部成员", Email: uniqueEmail("outsider"), Password: "password123"})
	ownerMeta := appservice.RequestMeta{Token: owner.Token, Locale: appservice.LocaleChineseSimplified}

	// 在第一个工作区修改的姓名作为新工作区的默认成员姓名。
	firstMeta := ownerMeta
	firstMeta.WorkspaceID = owner.Identity.Organization.ID
	if _, err := backend.UpdateProfile(ctx, firstMeta, appservice.ProfileInput{DisplayName: "改名后的负责人", Email: owner.Identity.Account.Email}); err != nil {
		t.Fatal(err)
	}
	second := addAccountWorkspace(t, db, owner.Token, "第二工作区").Organization
	secondMeta := ownerMeta
	secondMeta.WorkspaceID = second.ID
	if identity, err := backend.LoadIdentity(ctx, secondMeta); err != nil || identity.User.DisplayName != "改名后的负责人" {
		t.Fatalf("second workspace identity = %#v, err = %v", identity.User, err)
	}
	_, err = backend.CreateWorkspace(ctx, ownerMeta, appservice.WorkspaceInput{Name: "非法标识", Slug: "-bad"})
	requireFieldError(t, err, "slug", i18n.FieldWorkspaceSlugInvalid)

	workspaces, err := backend.ListWorkspaces(ctx, ownerMeta)
	if err != nil || len(workspaces.Items) != 2 {
		t.Fatalf("owner workspaces = %#v, err = %v", workspaces, err)
	}
	// 同一个会话按请求目标工作区解析出各自的成员身份。
	for _, workspace := range workspaces.Items {
		meta := ownerMeta
		meta.WorkspaceID = workspace.ID
		identity, err := backend.LoadIdentity(ctx, meta)
		if err != nil || identity.Organization.ID != workspace.ID {
			t.Fatalf("identity in %s = %#v, err = %v", workspace.Slug, identity, err)
		}
	}

	_, err = backend.LoadIdentity(ctx, ownerMeta)
	requireSessionState(t, err, appservice.SessionStateWorkspace)
	outsiderMeta := appservice.RequestMeta{Token: outsider.Token, WorkspaceID: second.ID, Locale: appservice.LocaleChineseSimplified}
	_, err = backend.LoadIdentity(ctx, outsiderMeta)
	requireSessionState(t, err, appservice.SessionStateWorkspace)
	_, err = backend.ListTeams(ctx, outsiderMeta, appservice.TeamListInput{})
	requireSessionState(t, err, appservice.SessionStateWorkspace)

	// 退出登录后该会话在所有工作区都失效。
	if err := backend.Logout(ctx, ownerMeta); err != nil {
		t.Fatal(err)
	}
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
	store, err := serverstorage.Open(context.Background(), servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	backend := newAccountTestBackend(db)
	ctx := context.Background()

	email := uniqueEmail("password")
	current := installWorkspace(t, db, workspaceSpec{Name: "密码工作区", DisplayName: "成员", Email: email, Password: "password123"})
	other := loginMember(t, db, current.Identity.Organization.ID, email, "password123")
	currentMeta := appservice.RequestMeta{Token: current.Token, WorkspaceID: current.Identity.Organization.ID, Locale: appservice.LocaleChineseSimplified}
	otherMeta := appservice.RequestMeta{Token: other.Token, WorkspaceID: current.Identity.Organization.ID, Locale: appservice.LocaleChineseSimplified}

	err = backend.ChangePassword(ctx, currentMeta, appservice.ChangePasswordInput{CurrentPassword: "wrong-password", NewPassword: "password456"})
	requireFieldError(t, err, "currentPassword", i18n.FieldCurrentPasswordIncorrect)
	if err := backend.ChangePassword(ctx, currentMeta, appservice.ChangePasswordInput{CurrentPassword: "password123", NewPassword: "password456"}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.LoadIdentity(ctx, currentMeta); err != nil {
		t.Fatalf("current session revoked: %v", err)
	}
	_, err = backend.LoadIdentity(ctx, otherMeta)
	requireSessionState(t, err, appservice.SessionStateLogin)
	loginMember(t, db, current.Identity.Organization.ID, email, "password456")
}
