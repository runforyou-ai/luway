//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"uuid"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
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

// newAccountTestBackend 创建未开放注册的自托管直接后端，只接入账号与工作区入口需要的依赖。
func newAccountTestBackend(db *bun.DB) *direct.Backend {
	return newRegistrationTestBackend(db, false)
}

// newRegistrationTestBackend 创建按指定注册开关配置的自托管直接后端。
func newRegistrationTestBackend(db *bun.DB, registrationOpen bool) *direct.Backend {
	return direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL, RegistrationOpen: registrationOpen},
		nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
}

// requireSessionState 断言错误把调用方引导到指定会话入口。
func requireSessionState(t *testing.T, err error, want appservice.SessionState) {
	t.Helper()
	if state := appservice.SessionStateOf(err); state != want {
		t.Fatalf("session state = %q, want %q (error: %v)", state, want, err)
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

// TestFirstInstallationAndRegistration 验证空部署进入初始化、首次安装只执行一次，以及注册按部署配置开放。
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
	// 开放注册的部署在首次安装之前也不能注册，第一个账号只能是部署管理员。
	early := appservice.RegisterInput{DisplayName: "抢先注册", Email: "early@example.test", Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai"}
	_, err = newRegistrationTestBackend(db, true).Register(ctx, meta, early)
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
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	if err != nil || len(workspaces.Items) != 1 || workspaces.Items[0].Slug != "demo-team" {
		t.Fatalf("workspaces = %#v, err = %v", workspaces, err)
	}
	adminMeta.WorkspaceID = workspaces.Items[0].ID
	identity, err := backend.LoadIdentity(ctx, adminMeta)
	if err != nil || identity.Organization.Slug != "demo-team" || identity.User.Email != "admin@example.test" || identity.User.DisplayName != "管理员" {
		t.Fatalf("identity = %#v, err = %v", identity, err)
	}

	// 部署未开放注册时拒绝注册，配置开放后可以注册。
	register := appservice.RegisterInput{DisplayName: "成员", Email: "member@example.test", Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai"}
	if _, err := backend.Register(ctx, meta, register); err == nil {
		t.Fatal("closed registration accepted a new account")
	}
	openBackend := newRegistrationTestBackend(db, true)
	status, err = openBackend.InstallationStatus(ctx, meta)
	if err != nil || !status.Installed || !status.RegistrationOpen {
		t.Fatalf("installed status = %#v, err = %v", status, err)
	}
	member, err := appservice.New(openBackend).Register(ctx, meta, register)
	if err != nil || member.Account.IsDeploymentAdmin {
		t.Fatalf("register auth = %#v, err = %v", member, err)
	}
	_, err = openBackend.Register(ctx, meta, register)
	requireFieldError(t, err, "email", i18n.FieldEmailDuplicate)
	memberMeta := appservice.RequestMeta{Token: member.Token, Locale: appservice.LocaleChineseSimplified}
	// 新注册账号没有任何工作区，进入工作区需要先创建或加入。
	workspaces, err = backend.ListWorkspaces(ctx, memberMeta)
	if err != nil || len(workspaces.Items) != 0 {
		t.Fatalf("member workspaces = %#v, err = %v", workspaces, err)
	}
	memberMeta.WorkspaceID = adminMeta.WorkspaceID
	_, err = backend.LoadIdentity(ctx, memberMeta)
	requireSessionState(t, err, appservice.SessionStateWorkspace)
}

// TestAccountWorkspacesAreIsolated 验证账号创建多个工作区后可分别进入，且无法进入不属于自己的工作区。
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
	slug := "second-" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:12]
	second, err := backend.CreateWorkspace(ctx, ownerMeta, appservice.WorkspaceInput{Name: "第二工作区", Slug: slug})
	if err != nil || second.Slug != slug {
		t.Fatalf("created workspace = %#v, err = %v", second, err)
	}
	secondMeta := ownerMeta
	secondMeta.WorkspaceID = second.ID
	if identity, err := backend.LoadIdentity(ctx, secondMeta); err != nil || identity.User.DisplayName != "改名后的负责人" {
		t.Fatalf("second workspace identity = %#v, err = %v", identity.User, err)
	}
	_, err = backend.CreateWorkspace(ctx, ownerMeta, appservice.WorkspaceInput{Name: "重复标识", Slug: slug})
	requireFieldError(t, err, "slug", i18n.FieldWorkspaceSlugTaken)
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
