//go:build server

package integrationtest

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
	"github.com/stretchr/testify/require"

	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/uptrace/bun"
)

// invitationFixture 是邀请测试使用的工作区管理员、后端与请求元数据。
type invitationFixture struct {
	db           *bun.DB
	backend      *direct.Backend
	owner        servertest.InstalledWorkspace
	ownerMeta    appservice.RequestMeta
	memberRoleID string
}

// newInvitationFixture 创建独立工作区与未开放注册的自托管后端。
func newInvitationFixture(t *testing.T) invitationFixture {
	t.Helper()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "邀请测试", DisplayName: "管理员", Email: servertest.UniqueEmail("inviter"), Password: "password123"})
	var memberRoleID string
	require.NoError(t, db.NewSelect().Model((*servermodels.Role)(nil)).ColumnExpr("id::text").
		Where("workspace_id = ? AND kind = ?", owner.Identity.Workspace.ID, domain.RoleKindMember).Scan(context.Background(), &memberRoleID))
	return invitationFixture{
		db: db, backend: newAccountTestBackend(t, db), owner: owner, memberRoleID: memberRoleID,
		ownerMeta: appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified},
	}
}

// invite 发起邀请并返回链接中的令牌。
func (f invitationFixture) invite(t *testing.T, email, displayName string) (appservice.InvitationCreated, string) {
	t.Helper()
	created, err := f.backend.CreateInvitation(context.Background(), f.ownerMeta, appservice.InvitationInput{Email: email, DisplayName: displayName, RoleID: f.memberRoleID})
	require.NoError(t, err)
	_, token, found := strings.Cut(created.Link, servertest.PublicURL+domain.WebAppPath+"#/invitations/")
	require.True(t, found, "invitation link = %q", created.Link)
	require.NotEmpty(t, token, "invitation link = %q", created.Link)
	return created, token
}

// requireErrorKey 断言错误是使用指定文案键的业务错误。
func requireErrorKey(t *testing.T, err error, key i18n.Key) {
	t.Helper()
	expected, _ := i18n.Localize("", key)
	var appError *appservice.Error
	require.ErrorAs(t, err, &appError, "错误应为 %s（%s）", key, expected)
	require.Equal(t, expected, appError.Message, "错误应为 %s", key)
}

// TestInvitationAcceptance 验证预览、邮箱一致校验、接受后加入工作区，以及同一邀请不能再次使用。
func TestInvitationAcceptance(t *testing.T) {
	t.Parallel()
	f := newInvitationFixture(t)
	ctx := context.Background()
	email := servertest.UniqueEmail("invitee")
	created, token := f.invite(t, strings.ToUpper(email), "受邀成员")
	require.Equal(t, email, created.Invitation.Email)
	require.Equal(t, domain.InvitationStatusPending, created.Invitation.Status)
	require.False(t, created.EmailQueued)
	list, err := f.backend.ListInvitations(ctx, f.ownerMeta)
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Equal(t, "管理员", list.Items[0].InviterName)
	preview, err := f.backend.PreviewInvitation(ctx, appservice.RequestMeta{}, appservice.InvitationTokenInput{Token: token})
	require.NoError(t, err)
	require.Equal(t, struct {
		WorkspaceName string
		WorkspaceSlug string
		InviterName   string
		MaskedEmail   string
	}{f.owner.Identity.Workspace.Name, f.owner.Identity.Workspace.Slug, "管理员", email[:1] + "***@" + strings.Split(email, "@")[1]}, struct {
		WorkspaceName string
		WorkspaceSlug string
		InviterName   string
		MaskedEmail   string
	}{preview.WorkspaceName, preview.WorkspaceSlug, preview.InviterName, preview.MaskedEmail})

	// 已是成员的账号打开任何邀请都提示已是成员。
	_, err = f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: f.owner.Token}, appservice.InvitationTokenInput{Token: token})
	requireErrorKey(t, err, i18n.ErrorInvitationAlreadyMember)

	// 其他邮箱的账号不能接受邀请。
	other := servertest.InstallWorkspace(t, f.db, servertest.WorkspaceSpec{Name: "其他工作区", DisplayName: "路人", Email: servertest.UniqueEmail("other"), Password: "password123"})
	_, err = f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: other.Token}, appservice.InvitationTokenInput{Token: token})
	requireErrorKey(t, err, i18n.ErrorInvitationEmailMismatch)

	// 受邀邮箱的账号接受后成为成员，邀请随即失效。
	invitee := servertest.InstallWorkspace(t, f.db, servertest.WorkspaceSpec{Name: "受邀人自己的工作区", DisplayName: "受邀人", Email: email, Password: "password123"})
	inviteeMeta := appservice.RequestMeta{Token: invitee.Token}
	workspace, err := f.backend.AcceptInvitation(ctx, inviteeMeta, appservice.InvitationTokenInput{Token: token})
	require.NoError(t, err)
	require.Equal(t, f.owner.Identity.Workspace.ID, workspace.ID)
	inviteeMeta.WorkspaceID = workspace.ID
	identity, err := f.backend.LoadIdentity(ctx, inviteeMeta)
	require.NoError(t, err)
	require.Equal(t, "受邀成员", identity.User.DisplayName)
	require.Equal(t, f.memberRoleID, identity.User.RoleID)
	workspaces, err := f.backend.ListWorkspaces(ctx, appservice.RequestMeta{Token: invitee.Token})
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 2)
	var verified bool
	require.NoError(t, f.db.NewSelect().Model((*servermodels.Account)(nil)).ColumnExpr("email_verified_at IS NOT NULL").
		Where("id = ?", invitee.Identity.Account.ID).Scan(ctx, &verified))
	require.True(t, verified, "invitee email verified")
	_, err = f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: invitee.Token}, appservice.InvitationTokenInput{Token: token})
	requireErrorKey(t, err, i18n.ErrorInvitationInvalid)
	list, err = f.backend.ListInvitations(ctx, f.ownerMeta)
	require.NoError(t, err)
	require.Empty(t, list.Items, "pending invitations after accept")

	// 已是成员的邮箱不能再被邀请。
	_, err = f.backend.CreateInvitation(ctx, f.ownerMeta, appservice.InvitationInput{Email: email, RoleID: f.memberRoleID})
	servertest.RequireFieldError(t, err, "email", i18n.FieldInvitationEmailMember)
}

// TestInvitationManagement 验证重复邀请、重新生成链接、撤销、过期后重新邀请、待接受邀请占用角色，以及其他工作区不能管理本工作区的邀请。
func TestInvitationManagement(t *testing.T) {
	t.Parallel()
	f := newInvitationFixture(t)
	ctx := context.Background()
	email := servertest.UniqueEmail("pending")
	first, firstToken := f.invite(t, email, "")
	_, err := f.backend.CreateInvitation(ctx, f.ownerMeta, appservice.InvitationInput{Email: email, RoleID: f.memberRoleID})
	servertest.RequireFieldError(t, err, "email", i18n.FieldInvitationEmailPending)

	// 重新生成链接后旧链接失效，新链接可以接受，未填写显示名称时使用账号名称。
	regenerated, err := f.backend.RegenerateInvitation(ctx, f.ownerMeta, first.Invitation.ID)
	require.NoError(t, err)
	require.NotEqual(t, first.Invitation.ID, regenerated.Invitation.ID)
	require.NotEqual(t, first.Link, regenerated.Link)
	_, err = f.backend.PreviewInvitation(ctx, appservice.RequestMeta{}, appservice.InvitationTokenInput{Token: firstToken})
	require.NoError(t, err, "revoked invitation preview failed")
	invitee := servertest.InstallWorkspace(t, f.db, servertest.WorkspaceSpec{Name: "受邀人工作区", DisplayName: "账号名称", Email: email, Password: "password123"})
	_, err = f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: invitee.Token}, appservice.InvitationTokenInput{Token: firstToken})
	requireErrorKey(t, err, i18n.ErrorInvitationInvalid)
	_, newToken, _ := strings.Cut(regenerated.Link, "#/invitations/")
	_, err = f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: invitee.Token}, appservice.InvitationTokenInput{Token: newToken})
	require.NoError(t, err)
	identity, err := f.backend.LoadIdentity(ctx, appservice.RequestMeta{Token: invitee.Token, WorkspaceID: f.owner.Identity.Workspace.ID})
	require.NoError(t, err)
	require.Equal(t, "账号名称", identity.User.DisplayName)

	// 撤销后链接失效；其他工作区的成员不能撤销本工作区的邀请。
	revoked, revokedToken := f.invite(t, servertest.UniqueEmail("revoked"), "")
	outsider := servertest.InstallWorkspace(t, f.db, servertest.WorkspaceSpec{Name: "外部工作区", DisplayName: "外部", Email: servertest.UniqueEmail("outsider"), Password: "password123"})
	outsiderMeta := appservice.RequestMeta{Token: outsider.Token, WorkspaceID: outsider.Identity.Workspace.ID}
	err = f.backend.RevokeInvitation(ctx, outsiderMeta, revoked.Invitation.ID)
	requireErrorKey(t, err, i18n.ErrorInvitationNotFound)
	require.NoError(t, f.backend.RevokeInvitation(ctx, f.ownerMeta, revoked.Invitation.ID))
	preview, err := f.backend.PreviewInvitation(ctx, appservice.RequestMeta{}, appservice.InvitationTokenInput{Token: revokedToken})
	require.NoError(t, err)
	require.Equal(t, domain.InvitationStatusRevoked, preview.Status)

	// 过期的邀请展示为过期，同一邮箱可以重新邀请。
	expiredEmail := servertest.UniqueEmail("expired")
	expired, _ := f.invite(t, expiredEmail, "")
	_, err = f.db.NewUpdate().Table("workspace_invitations").Set("expires_at = now() - interval '1 second'").Where("id = ?", expired.Invitation.ID).Exec(ctx)
	require.NoError(t, err)
	list, err := f.backend.ListInvitations(ctx, f.ownerMeta)
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Equal(t, domain.InvitationStatusExpired, list.Items[0].Status)
	f.invite(t, expiredEmail, "")

	// 待接受的邀请占用角色，撤销后角色才能删除。
	role, err := f.backend.CreateRole(ctx, f.ownerMeta, appservice.RoleInput{Name: "临时角色", Permissions: []appservice.PermissionCode{}})
	require.NoError(t, err)
	roleInvitation, err := f.backend.CreateInvitation(ctx, f.ownerMeta, appservice.InvitationInput{Email: servertest.UniqueEmail("role"), RoleID: role.ID})
	require.NoError(t, err)
	defaultLocaleMeta := f.ownerMeta
	defaultLocaleMeta.Locale = ""
	requireErrorKey(t, f.backend.DeleteRole(ctx, defaultLocaleMeta, role.ID), i18n.ErrorRoleInUse)
	require.NoError(t, f.backend.RevokeInvitation(ctx, f.ownerMeta, roleInvitation.Invitation.ID))
	require.NoError(t, f.backend.DeleteRole(ctx, f.ownerMeta, role.ID))
}

// TestInvitationRegistration 验证未开放注册的平台只允许用有效邀请注册受邀邮箱。
func TestInvitationRegistration(t *testing.T) {
	t.Parallel()
	f := newInvitationFixture(t)
	ctx := context.Background()
	email := servertest.UniqueEmail("register")
	_, token := f.invite(t, email, "")
	register := appservice.RegisterInput{DisplayName: "新成员", Email: email, Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai"}

	_, err := f.backend.Register(ctx, appservice.RequestMeta{}, register)
	requireErrorKey(t, err, i18n.ErrorRegistrationClosed)
	wrongEmail := register
	wrongEmail.Email, wrongEmail.InvitationToken = servertest.UniqueEmail("wrong"), token
	_, err = f.backend.Register(ctx, appservice.RequestMeta{}, wrongEmail)
	requireErrorKey(t, err, i18n.ErrorInvitationEmailMismatch)
	invalid := register
	invalid.InvitationToken = "invalid-token"
	_, err = f.backend.Register(ctx, appservice.RequestMeta{}, invalid)
	requireErrorKey(t, err, i18n.ErrorInvitationInvalid)

	register.InvitationToken = token
	auth, err := f.backend.Register(ctx, appservice.RequestMeta{}, register)
	require.NoError(t, err)
	workspace, err := f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: auth.Token}, appservice.InvitationTokenInput{Token: token})
	require.NoError(t, err)
	require.Equal(t, f.owner.Identity.Workspace.ID, workspace.ID)
}

// invitationEmailLinkPattern 匹配邀请邮件正文中的邀请链接令牌。
var invitationEmailLinkPattern = regexp.MustCompile(regexp.QuoteMeta(servertest.PublicURL+domain.WebAppPath+"#/invitations/") + `([A-Za-z0-9_-]+)`)

// queuedInvitationEmail 断言邀请在创建事务内投递了一个邀请邮件任务并返回其输入。
func queuedInvitationEmail(t *testing.T, tasks *servertest.Tasks, invitationID string) invitationaction.SendEmailInput {
	t.Helper()
	runs := tasks.Keyed(invitationaction.SendEmailActionName, "invitation-email:"+invitationID)
	require.Len(t, runs, 1, "invitation email task")
	require.Equal(t, 9, runs[0].Options.MaxAttempts)
	return servertest.TaskPayload[invitationaction.SendEmailInput](t, runs[0])
}

// emailedInvitationToken 返回邀请邮件正文中的邀请链接令牌。
func emailedInvitationToken(t *testing.T, message mail.Message) string {
	t.Helper()
	match := invitationEmailLinkPattern.FindStringSubmatch(message.Text)
	require.Len(t, match, 2, "invitation email text = %q", message.Text)
	return match[1]
}

// TestInvitationEmailTask 验证配置邮件发送时邀请事务内投递邮件任务，任务发信失败返回错误由任务重试，成功后邮件链接与复制链接都可接受邀请，撤销或过期的邀请不再发送。
func TestInvitationEmailTask(t *testing.T) {
	t.Parallel()
	f := newInvitationFixture(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	mailer := &servertest.RecordingMailSender{}
	publicURL := func() string { return servertest.PublicURL }
	create := invitationaction.NewCreateAction(f.db, tasks, seataction.Seats{}, mailer.Enabled, publicURL)
	send := invitationaction.NewSendEmailAction(f.db, mailer, publicURL)
	email := servertest.UniqueEmail("emailed")
	created, err := create.Execute(ctx, f.owner.Identity, invitationaction.Input{Email: email, RoleID: f.memberRoleID})
	require.NoError(t, err)
	require.True(t, created.Invitation.EmailDelivery)
	input := queuedInvitationEmail(t, tasks, created.Invitation.ID)
	require.Equal(t, invitationaction.SendEmailInput{WorkspaceID: f.owner.Identity.Workspace.ID, InvitationID: created.Invitation.ID, Locale: domain.Locale(f.owner.Identity.Account.Locale)}, input)

	// 发信失败时任务返回错误，由任务运行时按退避重试。
	mailer.Fail = errors.New("smtp unavailable")
	require.Error(t, send.Execute(ctx, input))
	require.Empty(t, mailer.Sent())
	mailer.Fail = nil
	require.NoError(t, send.Execute(ctx, input))
	sent := mailer.Sent()
	require.Len(t, sent, 1)
	require.Equal(t, email, sent[0].To)
	emailToken := emailedInvitationToken(t, sent[0])
	_, copyToken, _ := strings.Cut(created.Link, "#/invitations/")
	require.NotEqual(t, copyToken, emailToken)

	// 再次执行时替换邮件链接令牌，只有最后发出的邮件链接与复制链接有效。
	require.NoError(t, send.Execute(ctx, input))
	sent = mailer.Sent()
	require.Len(t, sent, 2)
	latestToken := emailedInvitationToken(t, sent[1])
	_, err = f.backend.PreviewInvitation(ctx, appservice.RequestMeta{}, appservice.InvitationTokenInput{Token: emailToken})
	requireErrorKey(t, err, i18n.ErrorInvitationInvalid)
	for _, value := range []string{copyToken, latestToken} {
		preview, err := f.backend.PreviewInvitation(ctx, appservice.RequestMeta{}, appservice.InvitationTokenInput{Token: value})
		require.NoError(t, err)
		require.Equal(t, domain.InvitationStatusPending, preview.Status)
	}
	invitee := servertest.InstallWorkspace(t, f.db, servertest.WorkspaceSpec{Name: "邮件受邀人工作区", DisplayName: "邮件受邀人", Email: email, Password: "password123"})
	workspace, err := f.backend.AcceptInvitation(ctx, appservice.RequestMeta{Token: invitee.Token}, appservice.InvitationTokenInput{Token: latestToken})
	require.NoError(t, err)
	require.Equal(t, f.owner.Identity.Workspace.ID, workspace.ID)

	// 已接受、已撤销或已过期的邀请执行任务时不发送邮件。
	require.NoError(t, send.Execute(ctx, input))
	revoked, err := create.Execute(ctx, f.owner.Identity, invitationaction.Input{Email: servertest.UniqueEmail("emailed-revoked"), RoleID: f.memberRoleID})
	require.NoError(t, err)
	require.NoError(t, invitationaction.NewRevokeAction(f.db).Execute(ctx, f.owner.Identity, revoked.Invitation.ID))
	require.NoError(t, send.Execute(ctx, queuedInvitationEmail(t, tasks, revoked.Invitation.ID)))
	expired, err := create.Execute(ctx, f.owner.Identity, invitationaction.Input{Email: servertest.UniqueEmail("emailed-expired"), RoleID: f.memberRoleID})
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Table("workspace_invitations").Set("expires_at = now() - interval '1 second'").Where("id = ?", expired.Invitation.ID).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, send.Execute(ctx, queuedInvitationEmail(t, tasks, expired.Invitation.ID)))
	require.Len(t, mailer.Sent(), 2)

	// 重新生成邀请时为新邀请投递邮件任务。
	pending, err := create.Execute(ctx, f.owner.Identity, invitationaction.Input{Email: servertest.UniqueEmail("emailed-regenerated"), RoleID: f.memberRoleID})
	require.NoError(t, err)
	regenerated, err := invitationaction.NewRegenerateAction(create).Execute(ctx, f.owner.Identity, pending.Invitation.ID)
	require.NoError(t, err)
	require.True(t, regenerated.Invitation.EmailDelivery)
	queuedInvitationEmail(t, tasks, regenerated.Invitation.ID)
}
