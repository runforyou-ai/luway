//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// invitationOps 持有成员邀请的 Action 和 Query。
type invitationOps struct {
	listInvitations      *invitationaction.ListQuery
	createInvitation     *invitationaction.CreateAction
	regenerateInvitation *invitationaction.RegenerateAction
	revokeInvitation     *invitationaction.RevokeAction
	previewInvitation    *invitationaction.PreviewQuery
	acceptInvitation     *invitationaction.AcceptAction
}

// newInvitationOps 创建成员邀请的业务实现依赖；emailEnabled 为假时只返回邀请链接，不投递邀请邮件任务。
func newInvitationOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, seats seataction.Seats, emailEnabled func() bool, publicURL func() string) *invitationOps {
	create := invitationaction.NewCreateAction(db, taskEnqueuer, seats, emailEnabled, publicURL)
	return &invitationOps{
		listInvitations:      invitationaction.NewListQuery(db),
		createInvitation:     create,
		regenerateInvitation: invitationaction.NewRegenerateAction(create),
		revokeInvitation:     invitationaction.NewRevokeAction(db),
		previewInvitation:    invitationaction.NewPreviewQuery(db),
		acceptInvitation:     invitationaction.NewAcceptAction(db, seats),
	}
}

// invitationFromModel 把邀请转换为应用契约。
func invitationFromModel(invitation invitationaction.Invitation) appservice.Invitation {
	return appservice.Invitation{
		ID: invitation.ID, Email: invitation.InvitedEmail, DisplayName: invitation.DisplayName,
		Role:   appservice.RoleSummary{ID: invitation.RoleID, Kind: invitation.RoleKind, Name: invitation.RoleName},
		Status: invitation.Status, InviterName: invitation.InviterName,
		ExpiresAt: invitation.ExpiresAt, CreatedAt: invitation.CreatedAt,
	}
}

// invitationCreatedFromModel 把新邀请和邀请链接转换为应用契约。
func invitationCreatedFromModel(created invitationaction.Created) appservice.InvitationCreated {
	return appservice.InvitationCreated{Invitation: invitationFromModel(created.Invitation), Link: created.Link, EmailQueued: created.Invitation.EmailDelivery}
}

// ListInvitations 返回当前工作区待接受的成员邀请。
func (o *invitationOps) ListInvitations(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.InvitationList, error) {
	invitations, err := o.listInvitations.Execute(ctx, identity)
	if err != nil {
		return appservice.InvitationList{}, invitationError(meta, err, i18n.ErrorInvitationListFailed)
	}
	items := arr.Map(invitations, invitationFromModel)
	return appservice.InvitationList{Items: items}, nil
}

// CreateInvitation 邀请账号加入当前工作区。
func (o *invitationOps) CreateInvitation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.InvitationInput) (appservice.InvitationCreated, error) {
	created, err := o.createInvitation.Execute(ctx, identity, invitationaction.Input{Email: input.Email, DisplayName: input.DisplayName, RoleID: input.RoleID})
	if err != nil {
		return appservice.InvitationCreated{}, invitationError(meta, err, i18n.ErrorInvitationCreateFailed)
	}
	slog.InfoContext(ctx, "成员邀请已创建", "invitation_id", created.Invitation.ID, "inviter_user_id", identity.User.ID)
	return invitationCreatedFromModel(created), nil
}

// RegenerateInvitation 撤销原邀请并以相同内容重新生成邀请链接。
func (o *invitationOps) RegenerateInvitation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, invitationID string) (appservice.InvitationCreated, error) {
	created, err := o.regenerateInvitation.Execute(ctx, identity, invitationID)
	if err != nil {
		return appservice.InvitationCreated{}, invitationError(meta, err, i18n.ErrorInvitationUpdateFailed)
	}
	slog.InfoContext(ctx, "成员邀请已重新生成", "previous_invitation_id", invitationID, "invitation_id", created.Invitation.ID)
	return invitationCreatedFromModel(created), nil
}

// RevokeInvitation 撤销待接受的邀请。
func (o *invitationOps) RevokeInvitation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, invitationID string) error {
	if err := o.revokeInvitation.Execute(ctx, identity, invitationID); err != nil {
		return invitationError(meta, err, i18n.ErrorInvitationUpdateFailed)
	}
	slog.InfoContext(ctx, "成员邀请已撤销", "invitation_id", invitationID)
	return nil
}

// PreviewInvitation 按邀请令牌返回邀请信息。
func (o *invitationOps) PreviewInvitation(ctx context.Context, meta appservice.RequestMeta, input appservice.InvitationTokenInput) (appservice.InvitationPreview, error) {
	preview, err := o.previewInvitation.Execute(ctx, input.Token)
	if errors.Is(err, invitationaction.ErrInvitationInvalid) {
		return appservice.InvitationPreview{}, appservice.NotFoundError(meta, i18n.ErrorInvitationInvalid)
	}
	if err != nil {
		return appservice.InvitationPreview{}, appservice.FailedError(meta, i18n.ErrorInvitationListFailed, err)
	}
	return appservice.InvitationPreview{WorkspaceName: preview.WorkspaceName, WorkspaceSlug: preview.WorkspaceSlug, InviterName: preview.InviterName, MaskedEmail: preview.MaskedEmail, Status: preview.Status}, nil
}

// AcceptInvitation 由当前账号接受邀请并加入工作区。
func (o *invitationOps) AcceptInvitation(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.InvitationTokenInput) (appservice.Workspace, error) {
	workspace, err := o.acceptInvitation.Execute(ctx, account, input.Token)
	switch {
	case errors.Is(err, invitationaction.ErrInvitationInvalid):
		return appservice.Workspace{}, appservice.InvalidError(meta, i18n.ErrorInvitationInvalid, nil)
	case errors.Is(err, invitationaction.ErrEmailMismatch):
		return appservice.Workspace{}, appservice.InvalidError(meta, i18n.ErrorInvitationEmailMismatch, nil)
	case errors.Is(err, invitationaction.ErrAlreadyMember):
		return appservice.Workspace{}, appservice.ConflictError(meta, i18n.ErrorInvitationAlreadyMember, "invitation_already_member")
	case errors.Is(err, seataction.ErrLimitReached):
		return appservice.Workspace{}, appservice.ConflictError(meta, i18n.ErrorSeatLimitReached, "seat_limit_reached")
	case err != nil:
		return appservice.Workspace{}, appservice.FailedError(meta, i18n.ErrorInvitationAcceptFailed, err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, workspace.ID), "账号已接受邀请")
	return appservice.Workspace{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug, Status: appservice.WorkspaceStatus(workspace.Status)}, nil
}

// invitationFieldKeys 把邀请校验错误码映射为本地化文案键。
var invitationFieldKeys = map[common.FieldCode]i18n.Key{
	invitationaction.ValidationEmailInvalid:       i18n.FieldEmailInvalid,
	invitationaction.ValidationEmailMember:        i18n.FieldInvitationEmailMember,
	invitationaction.ValidationEmailPending:       i18n.FieldInvitationEmailPending,
	invitationaction.ValidationDisplayNameInvalid: i18n.FieldDisplayNameInvalid,
	invitationaction.ValidationRoleInvalid:        i18n.FieldMemberRoleInvalid,
}

// invitationErrors 是邀请管理的错误转换规则，操作者身份失效时回到工作区入口。
var invitationErrors = dispatch.Catalog{
	dispatch.FieldRule(invitationFieldKeys),
	dispatch.Is(invitationaction.ErrNotFound, dispatch.NotFound(i18n.ErrorInvitationNotFound)),
	dispatch.Is(roleaction.ErrAdministratorOnly, dispatch.Forbidden(i18n.ErrorAdministratorOnly)),
	dispatch.Is(seataction.ErrLimitReached, dispatch.Conflict(i18n.ErrorSeatLimitReached, "seat_limit_reached")),
	dispatch.Is(identityaction.ErrInvalid, dispatch.Session(appservice.SessionStateWorkspace, i18n.ErrorWorkspaceUnavailable)),
}

// invitationError 把邀请管理的 Action 错误转成本地化业务错误。
func invitationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return invitationErrors.Translate(meta, err, failureKey)
}
