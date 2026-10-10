//go:build server

package integrationtest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestRolePermissionEnforcement 验证分发层按成员所属角色的权限放行或拒绝接口调用：管理员拥有全部权限且不写入角色权限关联，内置成员没有权限，只能调用无需权限的接口，改为自定义角色后立即按新角色的权限判断，工作区电脑只能由拥有工作区管理权限的成员撤销，修改角色权限后推进其成员的身份资料版本，权限不变时不推进；没有 AI 员工管理权限时 AI 员工目录只返回本人的个人 AI 员工。
func TestRolePermissionEnforcement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	backend := newAccountTestBackend(t, db)
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "权限测试", DisplayName: "管理员", Email: servertest.UniqueEmail("permission-owner"), Password: "password123"})
	workspaceID := owner.Identity.Workspace.ID
	ownerMeta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: workspaceID, Locale: domain.LocaleChineseSimplified}

	var memberRoleID string
	require.NoError(t, db.NewSelect().Model((*servermodels.Role)(nil)).ColumnExpr("id::text").
		Where("workspace_id = ? AND kind = ?", workspaceID, domain.RoleKindMember).Scan(ctx, &memberRoleID))
	email := servertest.UniqueEmail("permission-member")
	member, err := newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner.Identity, memberSpec{DisplayName: "成员", Email: email, Password: "password123", RoleID: memberRoleID})
	require.NoError(t, err)
	auth, err := backend.Login(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.LoginInput{Email: email, Password: "password123"})
	require.NoError(t, err)
	memberMeta := appservice.RequestMeta{Token: auth.Token, WorkspaceID: workspaceID, Locale: domain.LocaleChineseSimplified}

	// permissions 返回当前身份中所属角色的类型与权限。
	permissions := func(meta appservice.RequestMeta) (appservice.RoleKind, []appservice.PermissionCode) {
		t.Helper()
		identity, err := backend.LoadIdentity(ctx, meta)
		require.NoError(t, err)
		return identity.User.RoleKind, identity.User.Permissions
	}
	// requireDenied 断言调用因角色未授予权限而被拒绝。
	requireDenied := func(err error) {
		t.Helper()
		servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)
		servertest.RequireErrorMessage(t, err, i18n.ErrorPermissionDenied)
	}

	// 管理员拥有全部权限，角色权限关联中没有管理员角色的记录。
	kind, granted := permissions(ownerMeta)
	require.Equal(t, domain.RoleKindAdmin, kind)
	all := make([]appservice.PermissionCode, 0)
	for _, definition := range domain.PermissionDefinitions() {
		all = append(all, definition.Code)
	}
	if diff := cmp.Diff(all, granted); diff != "" {
		t.Fatalf("admin permissions mismatch (-want +got):\n%s", diff)
	}
	adminRows, err := db.NewSelect().TableExpr("role_permissions AS rp").
		Join("JOIN roles AS r ON r.id = rp.role_id").
		Where("r.workspace_id = ? AND r.kind = ?", workspaceID, domain.RoleKindAdmin).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, adminRows)
	roles, err := backend.ListRoles(ctx, ownerMeta)
	require.NoError(t, err)
	for _, role := range roles.Roles {
		if role.Kind == domain.RoleKindAdmin {
			require.Equal(t, all, role.Permissions)
		}
	}
	computer, err := backend.CreateWorkspaceComputer(ctx, ownerMeta, appservice.WorkspaceComputerInput{Name: "构建机"})
	require.NoError(t, err)

	// 内置成员没有任何权限，管理接口被拒绝，无需权限的接口照常可用。
	kind, granted = permissions(memberMeta)
	require.Equal(t, domain.RoleKindMember, kind)
	require.Empty(t, granted)
	_, err = backend.ListTeams(ctx, memberMeta, appservice.TeamListInput{})
	require.NoError(t, err)
	_, err = backend.ListUsers(ctx, memberMeta, appservice.UserListInput{})
	requireDenied(err)
	_, err = backend.ListMessageChannels(ctx, memberMeta)
	requireDenied(err)
	_, err = backend.CreateTeam(ctx, memberMeta, appservice.TeamInput{Name: "销售"})
	requireDenied(err)
	_, err = backend.ListRoles(ctx, memberMeta)
	requireDenied(err)
	// 没有 AI 员工管理权限时 AI 员工目录只返回本人的个人 AI 员工。
	agents, err := backend.ListAgents(ctx, memberMeta, appservice.AgentListInput{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Empty(t, agents.Agents)
	err = backend.RevokeComputer(ctx, memberMeta, computer.Computer.ID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)

	// 改为只授予工作区管理与客服管理的自定义角色后，立即按新角色判断，权限按目录顺序返回。
	custom, err := backend.CreateRole(ctx, ownerMeta, appservice.RoleInput{Name: "渠道运营", Permissions: []appservice.PermissionCode{
		domain.PermissionCustomerServiceManage, domain.PermissionWorkspaceManage,
	}})
	require.NoError(t, err)
	require.NoError(t, backend.UpdateRoleAssignments(ctx, ownerMeta, appservice.RoleAssignmentsInput{Assignments: []appservice.RoleAssignmentInput{
		{IdentityID: member.IdentityID, RoleID: custom.ID},
	}}))
	kind, granted = permissions(memberMeta)
	require.Equal(t, domain.RoleKindCustom, kind)
	require.Equal(t, []appservice.PermissionCode{domain.PermissionWorkspaceManage, domain.PermissionCustomerServiceManage}, granted)
	_, err = backend.ListMessageChannels(ctx, memberMeta)
	require.NoError(t, err)
	_, err = backend.ListUsers(ctx, memberMeta, appservice.UserListInput{})
	require.NoError(t, err)
	agents, err = backend.ListAgents(ctx, memberMeta, appservice.AgentListInput{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Empty(t, agents.Agents)
	require.NoError(t, backend.RevokeComputer(ctx, memberMeta, computer.Computer.ID))

	// 修改角色权限后推进其成员的身份资料版本，成员身份随即带出新的权限。
	profileVersion := func() int64 {
		t.Helper()
		var version int64
		require.NoError(t, db.NewSelect().Model((*servermodels.User)(nil)).Column("profile_version").Where("id = ?", member.ID).Scan(ctx, &version))
		return version
	}
	before := profileVersion()
	_, err = backend.UpdateRole(ctx, ownerMeta, custom.ID, appservice.RoleInput{Name: "渠道运营", Permissions: []appservice.PermissionCode{
		domain.PermissionCustomerServiceManage, domain.PermissionReportsView,
	}})
	require.NoError(t, err)
	require.Greater(t, profileVersion(), before)
	_, granted = permissions(memberMeta)
	require.Equal(t, []appservice.PermissionCode{domain.PermissionCustomerServiceManage, domain.PermissionReportsView}, granted)
	// 只修改名称、权限不变时不推进成员的身份资料版本。
	unchanged := profileVersion()
	_, err = backend.UpdateRole(ctx, ownerMeta, custom.ID, appservice.RoleInput{Name: "渠道专员", Permissions: []appservice.PermissionCode{
		domain.PermissionCustomerServiceManage, domain.PermissionReportsView,
	}})
	require.NoError(t, err)
	require.Equal(t, unchanged, profileVersion())
}

// TestAdministratorOnlyRoleChanges 验证涉及管理员的操作只有管理员可以做：非管理员不能把自己或他人调整为管理员，不能邀请、重新邀请或撤销管理员角色的邀请，不能停用或调整管理员；其余角色可以自由新建、修改与分配；角色选项按同一规则标出可分配的角色。
func TestAdministratorOnlyRoleChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	backend := newAccountTestBackend(t, db)
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "授权范围测试", DisplayName: "管理员", Email: servertest.UniqueEmail("within-owner"), Password: "password123"})
	workspaceID := owner.Identity.Workspace.ID
	ownerMeta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: workspaceID, Locale: domain.LocaleChineseSimplified}
	roleIDs := map[domain.RoleKind]string{}
	options, err := backend.ListRoleOptions(ctx, ownerMeta)
	require.NoError(t, err)
	for _, option := range options.Roles {
		roleIDs[option.Kind] = option.ID
		require.True(t, option.Assignable, "管理员可以分配 %s", option.Kind)
	}

	// 人事角色可以管理工作区，但没有其他权限。
	hr, err := backend.CreateRole(ctx, ownerMeta, appservice.RoleInput{Name: "人事", Permissions: []appservice.PermissionCode{domain.PermissionWorkspaceManage}})
	require.NoError(t, err)
	email := servertest.UniqueEmail("within-hr")
	actor, err := newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner.Identity, memberSpec{DisplayName: "人事", Email: email, Password: "password123", RoleID: hr.ID})
	require.NoError(t, err)
	auth, err := backend.Login(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.LoginInput{Email: email, Password: "password123"})
	require.NoError(t, err)
	actorMeta := appservice.RequestMeta{Token: auth.Token, WorkspaceID: workspaceID, Locale: domain.LocaleChineseSimplified}
	// requireAdministrator 断言操作因只有管理员可以操作而被拒绝。
	requireAdministrator := func(err error) {
		t.Helper()
		servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)
		servertest.RequireErrorMessage(t, err, i18n.ErrorAdministratorOnly)
	}

	// 角色选项：除管理员外的角色都可以分配。
	options, err = backend.ListRoleOptions(ctx, actorMeta)
	require.NoError(t, err)
	assignable := arr.Associate(options.Roles, func(option appservice.RoleOption) (string, bool) { return option.ID, option.Assignable })
	require.Equal(t, map[string]bool{
		roleIDs[domain.RoleKindAdmin]: false, roleIDs[domain.RoleKindCustomerService]: true, roleIDs[domain.RoleKindMember]: true, hr.ID: true,
	}, assignable)

	// 不能把自己调整为管理员，管理员的角色、状态也不能被人事调整。
	requireAdministrator(backend.UpdateRoleAssignments(ctx, actorMeta, appservice.RoleAssignmentsInput{Assignments: []appservice.RoleAssignmentInput{
		{IdentityID: actor.IdentityID, RoleID: roleIDs[domain.RoleKindAdmin]},
	}}))
	_, err = backend.UpdateUser(ctx, actorMeta, actor.ID, appservice.UpdateUserInput{DisplayName: "人事", RoleID: roleIDs[domain.RoleKindAdmin], TeamIDs: []string{}})
	requireAdministrator(err)
	requireAdministrator(backend.UpdateRoleAssignments(ctx, actorMeta, appservice.RoleAssignmentsInput{Assignments: []appservice.RoleAssignmentInput{
		{IdentityID: owner.Identity.WorkspaceIdentity.ID, RoleID: hr.ID},
	}}))
	_, err = backend.DeactivateUser(ctx, actorMeta, owner.Identity.User.ID)
	requireAdministrator(err)
	identity, err := backend.LoadIdentity(ctx, actorMeta)
	require.NoError(t, err)
	require.Equal(t, hr.ID, identity.User.RoleID)

	// 不能邀请为管理员，管理员发出的管理员邀请也不能由人事重新生成或撤销。
	_, err = backend.CreateInvitation(ctx, actorMeta, appservice.InvitationInput{Email: servertest.UniqueEmail("within-invite-admin"), RoleID: roleIDs[domain.RoleKindAdmin]})
	requireAdministrator(err)
	_, err = backend.CreateInvitation(ctx, actorMeta, appservice.InvitationInput{Email: servertest.UniqueEmail("within-invite-member"), RoleID: roleIDs[domain.RoleKindMember]})
	require.NoError(t, err)
	adminInvitation, err := backend.CreateInvitation(ctx, ownerMeta, appservice.InvitationInput{Email: servertest.UniqueEmail("within-invite-owner"), RoleID: roleIDs[domain.RoleKindAdmin]})
	require.NoError(t, err)
	_, err = backend.RegenerateInvitation(ctx, actorMeta, adminInvitation.Invitation.ID)
	requireAdministrator(err)
	requireAdministrator(backend.RevokeInvitation(ctx, actorMeta, adminInvitation.Invitation.ID))

	// 管理员以外的角色可以自由新建、修改并分配给自己。
	reports, err := backend.CreateRole(ctx, actorMeta, appservice.RoleInput{Name: "报表", Permissions: []appservice.PermissionCode{domain.PermissionReportsView}})
	require.NoError(t, err)
	_, err = backend.UpdateRole(ctx, actorMeta, hr.ID, appservice.RoleInput{Name: "人事", Permissions: []appservice.PermissionCode{
		domain.PermissionWorkspaceManage, domain.PermissionReportsView,
	}})
	require.NoError(t, err)
	_, err = backend.UpdateRole(ctx, actorMeta, roleIDs[domain.RoleKindCustomerService], appservice.RoleInput{Permissions: []appservice.PermissionCode{}})
	require.NoError(t, err)
	require.NoError(t, backend.DeleteRole(ctx, actorMeta, reports.ID))
	require.NoError(t, backend.UpdateRoleAssignments(ctx, actorMeta, appservice.RoleAssignmentsInput{Assignments: []appservice.RoleAssignmentInput{
		{IdentityID: actor.IdentityID, RoleID: roleIDs[domain.RoleKindCustomerService]},
	}}))
}

// TestRoleChangeUsesCurrentActorRole 验证是否管理员按操作者在写事务中的最新角色判断：管理员被降权的事务提交前已发出、正在等待行锁的请求，不能把自己恢复为管理员。
func TestRoleChangeUsesCurrentActorRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	backend := newAccountTestBackend(t, db)
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "降权并发测试", DisplayName: "管理员", Email: servertest.UniqueEmail("race-owner"), Password: "password123"})
	ownerMeta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	hr, err := backend.CreateRole(ctx, ownerMeta, appservice.RoleInput{Name: "人事", Permissions: []appservice.PermissionCode{domain.PermissionWorkspaceManage}})
	require.NoError(t, err)
	email := servertest.UniqueEmail("race-actor")
	actor, err := newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner.Identity, memberSpec{DisplayName: "待降权管理员", Email: email, Password: "password123", RoleID: owner.Identity.User.RoleID})
	require.NoError(t, err)
	auth, err := backend.Login(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.LoginInput{Email: email, Password: "password123"})
	require.NoError(t, err)
	actorMeta := appservice.RequestMeta{Token: auth.Token, WorkspaceID: ownerMeta.WorkspaceID, Locale: domain.LocaleChineseSimplified}

	// 降权事务写入成员角色后暂停，持有目标成员的行锁直到放行。
	hook := &pausingUserUpdateHook{written: make(chan struct{}), release: make(chan struct{})}
	db.AddQueryHook(hook)
	demoted := make(chan error, 1)
	go func() {
		demoted <- backend.UpdateRoleAssignments(context.WithValue(ctx, pausingUserUpdateKey{}, true), ownerMeta, appservice.RoleAssignmentsInput{
			Assignments: []appservice.RoleAssignmentInput{{IdentityID: actor.IdentityID, RoleID: hr.ID}},
		})
	}()
	select {
	case <-hook.written:
	case <-time.After(10 * time.Second):
		t.Fatal("降权事务未写入成员角色")
	}
	restored := make(chan error, 1)
	go func() {
		_, err := backend.UpdateUser(ctx, actorMeta, actor.ID, appservice.UpdateUserInput{DisplayName: "待降权管理员", RoleID: owner.Identity.User.RoleID, TeamIDs: []string{}})
		restored <- err
	}()
	// 等待恢复请求阻塞在成员行锁上再放行降权事务。
	require.Eventually(t, func() bool {
		var waiting int
		err := db.NewRaw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE ?", "%"+actor.ID+"%").Scan(ctx, &waiting)
		return err == nil && waiting > 0
	}, 10*time.Second, 20*time.Millisecond)
	close(hook.release)
	require.NoError(t, <-demoted)

	err = <-restored
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)
	servertest.RequireErrorMessage(t, err, i18n.ErrorAdministratorOnly)
	identity, err := backend.LoadIdentity(ctx, actorMeta)
	require.NoError(t, err)
	require.Equal(t, hr.ID, identity.User.RoleID)
}

// pausingUserUpdateKey 标记需要在写入成员行后暂停的请求。
type pausingUserUpdateKey struct{}

// pausingUserUpdateHook 在带标记的请求写入成员行后暂停，直到 release 关闭。
type pausingUserUpdateHook struct {
	once    sync.Once
	written chan struct{}
	release chan struct{}
}

// BeforeQuery 不处理查询前事件。
func (h *pausingUserUpdateHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

// AfterQuery 在带标记的请求首次更新成员行后通知并等待放行。
func (h *pausingUserUpdateHook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if ctx.Value(pausingUserUpdateKey{}) != true || !strings.Contains(event.Query, `UPDATE "users"`) {
		return
	}
	h.once.Do(func() {
		close(h.written)
		<-h.release
	})
}

// TestRoleEditAndAssignmentDoNotDeadlock 验证编辑角色与把成员分配到该角色并发时不会死锁：两个请求各自取得角色行锁后暂停，同时放行后都成功。
func TestRoleEditAndAssignmentDoNotDeadlock(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	backend := newAccountTestBackend(t, db)
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "角色锁顺序测试", DisplayName: "管理员一", Email: servertest.UniqueEmail("lock-order-owner"), Password: "password123"})
	ownerMeta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	role, err := backend.CreateRole(ctx, ownerMeta, appservice.RoleInput{Name: "待分配", Permissions: []appservice.PermissionCode{}})
	require.NoError(t, err)
	email := servertest.UniqueEmail("lock-order-admin")
	second, err := newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner.Identity, memberSpec{DisplayName: "管理员二", Email: email, Password: "password123", RoleID: owner.Identity.User.RoleID})
	require.NoError(t, err)
	auth, err := backend.Login(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.LoginInput{Email: email, Password: "password123"})
	require.NoError(t, err)
	secondMeta := appservice.RequestMeta{Token: auth.Token, WorkspaceID: ownerMeta.WorkspaceID, Locale: domain.LocaleChineseSimplified}

	// 编辑请求锁定被编辑的角色、分配请求锁定管理员角色后各自暂停，两者都到达后同时放行。
	hook := &pausingRoleLockHook{acquired: make(chan struct{}, 2), release: make(chan struct{})}
	db.AddQueryHook(hook)
	edited := make(chan error, 1)
	go func() {
		_, err := backend.UpdateRole(context.WithValue(ctx, pausingRoleLockKey{}, true), ownerMeta, role.ID, appservice.RoleInput{Name: "已改名", Permissions: []appservice.PermissionCode{}})
		edited <- err
	}()
	assigned := make(chan error, 1)
	go func() {
		assigned <- backend.UpdateRoleAssignments(context.WithValue(ctx, pausingRoleLockKey{}, true), secondMeta, appservice.RoleAssignmentsInput{
			Assignments: []appservice.RoleAssignmentInput{{IdentityID: second.IdentityID, RoleID: role.ID}},
		})
	}()
	for range 2 {
		select {
		case <-hook.acquired:
		case <-ctx.Done():
			close(hook.release)
			t.Fatal(ctx.Err())
		}
	}
	close(hook.release)
	require.NoError(t, <-edited)
	require.NoError(t, <-assigned)
}

// pausingRoleLockKey 标记需要在取得角色行锁后暂停的请求。
type pausingRoleLockKey struct{}

// pausingRoleLockHook 在带标记的请求首次以 FOR UPDATE 锁定角色行后通知并等待放行。
type pausingRoleLockHook struct {
	acquired chan struct{}
	release  chan struct{}
}

// BeforeQuery 不处理查询前事件。
func (h *pausingRoleLockHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

// AfterQuery 在带标记的请求锁定角色行后通知并等待放行。
func (h *pausingRoleLockHook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if ctx.Value(pausingRoleLockKey{}) != true || event.Err != nil ||
		!strings.Contains(event.Query, `FROM "roles"`) || !strings.HasSuffix(event.Query, "FOR UPDATE") {
		return
	}
	select {
	case h.acquired <- struct{}{}:
	default:
	}
	select {
	case <-h.release:
	case <-ctx.Done():
	}
}
