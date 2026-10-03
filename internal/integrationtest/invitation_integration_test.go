//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// invitationFixture 是邀请测试使用的工作区管理员、后端与请求元数据。
type invitationFixture struct {
	db           *bun.DB
	backend      *direct.Backend
	owner        installedWorkspace
	ownerMeta    appservice.RequestMeta
	memberRoleID string
}

// newInvitationFixture 创建独立工作区与未开放注册的自托管后端。
func newInvitationFixture(t *testing.T) invitationFixture {
	t.Helper()
	store, err := serverstorage.Open(context.Background(), servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	owner := installWorkspace(t, db, workspaceSpec{Name: "邀请测试", DisplayName: "管理员", Email: uniqueEmail("inviter"), Password: "password123"})
	var memberRoleID string
	if err := db.NewSelect().Model((*servermodels.Role)(nil)).ColumnExpr("id::text").
		Where("organization_id = ? AND kind = ?", owner.Identity.Organization.ID, domain.RoleKindMember).Scan(context.Background(), &memberRoleID); err != nil {
		t.Fatal(err)
	}
	return invitationFixture{
		db: db, backend: newAccountTestBackend(db), owner: owner, memberRoleID: memberRoleID,
		ownerMeta: appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Organization.ID, Locale: appservice.LocaleChineseSimplified},
	}
}

// invite 发起邀请并返回链接中的令牌。
func (f invitationFixture) invite(t *testing.T, email, displayName string) (appservice.InvitationCreated, string) {
	t.Helper()
	created, err := f.backend.CreateInvitation(context.Background(), f.ownerMeta, appservice.InvitationInput{Email: email, DisplayName: displayName, RoleID: f.memberRoleID})
	if err != nil {
		t.Fatal(err)
	}
	_, token, found := strings.Cut(created.Link, testPublicURL+domain.WebAppPath+"#/invitations/")
	if !found || token == "" {
		t.Fatalf("invitation link = %q", created.Link)
	}
	return created, token
}

// requireErrorKey 断言错误是使用指定文案键的业务错误。
func requireErrorKey(t *testing.T, err error, key i18n.Key) {
	t.Helper()
	expected, _ := i18n.Localize("", key)
	var appError *appservice.Error
	if !errors.As(err, &appError) || appError.Message != expected {
		t.Fatalf("错误应为 %s（%s），实际为 %v", key, expected, err)
	}
}

// TestInvitationAcceptance 验证预览、邮箱一致校验、接受后加入工作区，以及同一邀请不能再次使用。
func TestInvitationAcceptance(t *testing.T) {
	t.Parallel()
	f := newInvitationFixture(t)
	ctx := context.Background()
	email := uniqueEmail("invitee")
	created, token := f.invite(t, strings.ToUpper(email), "受邀成员")
	if created.Invitation.Email != email || created.Invitation.Status != appservice.InvitationStatusPending || created.EmailQueued {
		t.Fatalf("created invitation = %#v", created)
	}
	list, err := f.backend.ListInvitations(ctx, f.ownerMeta)
	if err != nil || len(list.Items) != 1 || list.Items[0].InviterName != "管理员" {
		t.Fatalf("invitation list = %#v, err = %v", list, err)
	}
	preview, err := f.backend.PreviewInvitation(ctx, appservice.RequestMeta{}, appservice.InvitationTokenInput{Token: token})
	if err != nil || preview.WorkspaceName != "邀请测试" || preview.WorkspaceSlug != f.owner.Identity.Organization.Slug || preview.InviterName != "管理员" ||
		preview.MaskedEmail != email[:1]+"***@"+strings.Split(email, "@")[1] {
		t.Fatalf("preview = %#v, err = %v", preview, err)
	}

	// 已是成员的账号打开任何邀请都提示已是成员。
	_, err = f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: f.owner.Token}, appservice.InvitationTokenInput{Token: token})
	requireErrorKey(t, err, i18n.ErrorInvitationAlreadyMember)

	// 其他邮箱的账号不能接受邀请。
	other := installWorkspace(t, f.db, workspaceSpec{Name: "其他工作区", DisplayName: "路人", Email: uniqueEmail("other"), Password: "password123"})
	_, err = f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: other.Token}, appservice.InvitationTokenInput{Token: token})
	requireErrorKey(t, err, i18n.ErrorInvitationEmailMismatch)

	// 受邀邮箱的账号接受后成为成员，邀请随即失效。
	invitee := installWorkspace(t, f.db, workspaceSpec{Name: "受邀人自己的工作区", DisplayName: "受邀人", Email: email, Password: "password123"})
	inviteeMeta := appservice.RequestMeta{Token: invitee.Token}
	workspace, err := f.backend.AcceptInvitation(ctx, inviteeMeta, appservice.InvitationTokenInput{Token: token})
	if err != nil || workspace.ID != f.owner.Identity.Organization.ID {
		t.Fatalf("accepted workspace = %#v, err = %v", workspace, err)
	}
	inviteeMeta.WorkspaceID = workspace.ID
	identity, err := f.backend.LoadIdentity(ctx, inviteeMeta)
	if err != nil || identity.User.DisplayName != "受邀成员" || identity.User.RoleID != f.memberRoleID {
		t.Fatalf("member identity = %#v, err = %v", identity.User, err)
	}
	workspaces, err := f.backend.ListWorkspaces(ctx, appservice.RequestMeta{Token: invitee.Token})
	if err != nil || len(workspaces.Items) != 2 {
		t.Fatalf("invitee workspaces = %#v, err = %v", workspaces, err)
	}
	var verified bool
	if err := f.db.NewSelect().Model((*servermodels.Account)(nil)).ColumnExpr("email_verified_at IS NOT NULL").
		Where("id = ?", invitee.Identity.Account.ID).Scan(ctx, &verified); err != nil || !verified {
		t.Fatalf("invitee email verified = %v, err = %v", verified, err)
	}
	_, err = f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: invitee.Token}, appservice.InvitationTokenInput{Token: token})
	requireErrorKey(t, err, i18n.ErrorInvitationInvalid)
	list, err = f.backend.ListInvitations(ctx, f.ownerMeta)
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("pending invitations after accept = %#v, err = %v", list, err)
	}

	// 已是成员的邮箱不能再被邀请。
	_, err = f.backend.CreateInvitation(ctx, f.ownerMeta, appservice.InvitationInput{Email: email, RoleID: f.memberRoleID})
	requireFieldError(t, err, "email", i18n.FieldInvitationEmailMember)
}

// TestInvitationManagement 验证重复邀请、重新生成链接、撤销、过期后重新邀请、待接受邀请占用角色，以及其他工作区不能管理本工作区的邀请。
func TestInvitationManagement(t *testing.T) {
	t.Parallel()
	f := newInvitationFixture(t)
	ctx := context.Background()
	email := uniqueEmail("pending")
	first, firstToken := f.invite(t, email, "")
	_, err := f.backend.CreateInvitation(ctx, f.ownerMeta, appservice.InvitationInput{Email: email, RoleID: f.memberRoleID})
	requireFieldError(t, err, "email", i18n.FieldInvitationEmailPending)

	// 重新生成链接后旧链接失效，新链接可以接受，未填写显示名称时使用账号名称。
	regenerated, err := f.backend.RegenerateInvitation(ctx, f.ownerMeta, first.Invitation.ID)
	if err != nil || regenerated.Invitation.ID == first.Invitation.ID || regenerated.Link == first.Link {
		t.Fatalf("regenerated = %#v, err = %v", regenerated, err)
	}
	_, err = f.backend.PreviewInvitation(ctx, appservice.RequestMeta{}, appservice.InvitationTokenInput{Token: firstToken})
	if err != nil {
		t.Fatalf("revoked invitation preview failed: %v", err)
	}
	invitee := installWorkspace(t, f.db, workspaceSpec{Name: "受邀人工作区", DisplayName: "账号名称", Email: email, Password: "password123"})
	_, err = f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: invitee.Token}, appservice.InvitationTokenInput{Token: firstToken})
	requireErrorKey(t, err, i18n.ErrorInvitationInvalid)
	_, newToken, _ := strings.Cut(regenerated.Link, "#/invitations/")
	if _, err := f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: invitee.Token}, appservice.InvitationTokenInput{Token: newToken}); err != nil {
		t.Fatal(err)
	}
	identity, err := f.backend.LoadIdentity(ctx, appservice.RequestMeta{Token: invitee.Token, WorkspaceID: f.owner.Identity.Organization.ID})
	if err != nil || identity.User.DisplayName != "账号名称" {
		t.Fatalf("member display name = %#v, err = %v", identity.User, err)
	}

	// 撤销后链接失效；其他工作区的成员不能撤销本工作区的邀请。
	revoked, revokedToken := f.invite(t, uniqueEmail("revoked"), "")
	outsider := installWorkspace(t, f.db, workspaceSpec{Name: "外部工作区", DisplayName: "外部", Email: uniqueEmail("outsider"), Password: "password123"})
	outsiderMeta := appservice.RequestMeta{Token: outsider.Token, WorkspaceID: outsider.Identity.Organization.ID}
	err = f.backend.RevokeInvitation(ctx, outsiderMeta, revoked.Invitation.ID)
	requireErrorKey(t, err, i18n.ErrorInvitationNotFound)
	if err := f.backend.RevokeInvitation(ctx, f.ownerMeta, revoked.Invitation.ID); err != nil {
		t.Fatal(err)
	}
	preview, err := f.backend.PreviewInvitation(ctx, appservice.RequestMeta{}, appservice.InvitationTokenInput{Token: revokedToken})
	if err != nil || preview.Status != appservice.InvitationStatusRevoked {
		t.Fatalf("revoked preview = %#v, err = %v", preview, err)
	}

	// 过期的邀请展示为过期，同一邮箱可以重新邀请。
	expiredEmail := uniqueEmail("expired")
	expired, _ := f.invite(t, expiredEmail, "")
	if _, err := f.db.NewUpdate().Table("organization_invitations").Set("expires_at = now() - interval '1 second'").Where("id = ?", expired.Invitation.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := f.backend.ListInvitations(ctx, f.ownerMeta)
	if err != nil || len(list.Items) != 1 || list.Items[0].Status != appservice.InvitationStatusExpired {
		t.Fatalf("invitations with expired = %#v, err = %v", list, err)
	}
	f.invite(t, expiredEmail, "")

	// 待接受的邀请占用角色，撤销后角色才能删除。
	role, err := f.backend.CreateRole(ctx, f.ownerMeta, appservice.RoleInput{Name: "临时角色", Permissions: []appservice.PermissionCode{}})
	if err != nil {
		t.Fatal(err)
	}
	roleInvitation, err := f.backend.CreateInvitation(ctx, f.ownerMeta, appservice.InvitationInput{Email: uniqueEmail("role"), RoleID: role.ID})
	if err != nil {
		t.Fatal(err)
	}
	defaultLocaleMeta := f.ownerMeta
	defaultLocaleMeta.Locale = ""
	requireErrorKey(t, f.backend.DeleteRole(ctx, defaultLocaleMeta, role.ID), i18n.ErrorRoleInUse)
	if err := f.backend.RevokeInvitation(ctx, f.ownerMeta, roleInvitation.Invitation.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.DeleteRole(ctx, f.ownerMeta, role.ID); err != nil {
		t.Fatal(err)
	}
}

// TestInvitationRegistration 验证未开放注册的平台只允许用有效邀请注册受邀邮箱。
func TestInvitationRegistration(t *testing.T) {
	t.Parallel()
	f := newInvitationFixture(t)
	ctx := context.Background()
	email := uniqueEmail("register")
	_, token := f.invite(t, email, "")
	register := appservice.RegisterInput{DisplayName: "新成员", Email: email, Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai"}

	_, err := f.backend.Register(ctx, appservice.RequestMeta{}, register)
	requireErrorKey(t, err, i18n.ErrorRegistrationClosed)
	wrongEmail := register
	wrongEmail.Email, wrongEmail.InvitationToken = uniqueEmail("wrong"), token
	_, err = f.backend.Register(ctx, appservice.RequestMeta{}, wrongEmail)
	requireErrorKey(t, err, i18n.ErrorInvitationEmailMismatch)
	invalid := register
	invalid.InvitationToken = "invalid-token"
	_, err = f.backend.Register(ctx, appservice.RequestMeta{}, invalid)
	requireErrorKey(t, err, i18n.ErrorInvitationInvalid)

	register.InvitationToken = token
	auth, err := f.backend.Register(ctx, appservice.RequestMeta{}, register)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: auth.Token}, appservice.InvitationTokenInput{Token: token})
	if err != nil || workspace.ID != f.owner.Identity.Organization.ID {
		t.Fatalf("accepted workspace = %#v, err = %v", workspace, err)
	}
}
