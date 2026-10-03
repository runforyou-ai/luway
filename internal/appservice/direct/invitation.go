//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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

// newInvitationOps 创建成员邀请的业务实现依赖，mailer 为空时只返回邀请链接。
func newInvitationOps(db *bun.DB, mailer invitationaction.Mailer, publicURL string) invitationOps {
	create := invitationaction.NewCreateAction(db, mailer, publicURL)
	return invitationOps{
		listInvitations:      invitationaction.NewListQuery(db),
		createInvitation:     create,
		regenerateInvitation: invitationaction.NewRegenerateAction(create),
		revokeInvitation:     invitationaction.NewRevokeAction(db),
		previewInvitation:    invitationaction.NewPreviewQuery(db),
		acceptInvitation:     invitationaction.NewAcceptAction(db),
	}
}

// invitationFromModel 把邀请转换为应用契约。
func invitationFromModel(invitation invitationaction.Invitation) appservice.Invitation {
	return appservice.Invitation{
		ID: invitation.ID, Email: invitation.InvitedEmail, DisplayName: invitation.DisplayName,
		Role:   appservice.RoleSummary{ID: invitation.RoleID, Kind: appservice.RoleKind(invitation.RoleKind), Name: invitation.RoleName},
		Status: appservice.InvitationStatus(invitation.Status), InviterName: invitation.InviterName,
		ExpiresAt: invitation.ExpiresAt, CreatedAt: invitation.CreatedAt,
	}
}

// invitationCreatedFromModel 把新邀请和邀请链接转换为应用契约。
func invitationCreatedFromModel(created invitationaction.Created) appservice.InvitationCreated {
	return appservice.InvitationCreated{Invitation: invitationFromModel(created.Invitation), Link: created.Link, EmailQueued: created.Invitation.EmailDelivery}
}

// ListInvitations 返回当前工作区待接受的成员邀请。
func (o *directOperations) ListInvitations(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.InvitationList, error) {
	invitations, err := o.listInvitations.Execute(ctx, identity)
	if err != nil {
		return appservice.InvitationList{}, o.invitationError(ctx, meta, err, i18n.ErrorInvitationListFailed, identity)
	}
	items := make([]appservice.Invitation, 0, len(invitations))
	for _, invitation := range invitations {
		items = append(items, invitationFromModel(invitation))
	}
	return appservice.InvitationList{Items: items}, nil
}

// CreateInvitation 邀请账号加入当前工作区。
func (o *directOperations) CreateInvitation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.InvitationInput) (appservice.InvitationCreated, error) {
	created, err := o.createInvitation.Execute(ctx, identity, invitationaction.Input{Email: input.Email, DisplayName: input.DisplayName, RoleID: input.RoleID})
	if err != nil {
		return appservice.InvitationCreated{}, o.invitationError(ctx, meta, err, i18n.ErrorInvitationCreateFailed, identity)
	}
	slog.Info("成员邀请已创建", "organization_id", identity.Organization.ID, "invitation_id", created.Invitation.ID, "inviter_user_id", identity.User.ID)
	return invitationCreatedFromModel(created), nil
}

// RegenerateInvitation 撤销原邀请并以相同内容重新生成邀请链接。
func (o *directOperations) RegenerateInvitation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, invitationID string) (appservice.InvitationCreated, error) {
	created, err := o.regenerateInvitation.Execute(ctx, identity, invitationID)
	if err != nil {
		return appservice.InvitationCreated{}, o.invitationError(ctx, meta, err, i18n.ErrorInvitationUpdateFailed, identity)
	}
	slog.Info("成员邀请已重新生成", "organization_id", identity.Organization.ID, "previous_invitation_id", invitationID, "invitation_id", created.Invitation.ID)
	return invitationCreatedFromModel(created), nil
}

// RevokeInvitation 撤销待接受的邀请。
func (o *directOperations) RevokeInvitation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, invitationID string) error {
	if err := o.revokeInvitation.Execute(ctx, identity, invitationID); err != nil {
		return o.invitationError(ctx, meta, err, i18n.ErrorInvitationUpdateFailed, identity)
	}
	slog.Info("成员邀请已撤销", "organization_id", identity.Organization.ID, "invitation_id", invitationID)
	return nil
}

// PreviewInvitation 按邀请令牌返回邀请信息。
func (o *directOperations) PreviewInvitation(ctx context.Context, meta appservice.RequestMeta, input appservice.InvitationTokenInput) (appservice.InvitationPreview, error) {
	preview, err := o.previewInvitation.Execute(ctx, input.Token)
	if errors.Is(err, invitationaction.ErrInvitationInvalid) {
		return appservice.InvitationPreview{}, appservice.NotFoundError(meta, i18n.ErrorInvitationInvalid)
	}
	if err != nil {
		if ctx.Err() != nil {
			return appservice.InvitationPreview{}, ctx.Err()
		}
		slog.Warn("读取邀请预览失败", "error", err)
		return appservice.InvitationPreview{}, appservice.FailedError(meta, i18n.ErrorInvitationListFailed)
	}
	return appservice.InvitationPreview{WorkspaceName: preview.WorkspaceName, WorkspaceSlug: preview.WorkspaceSlug, InviterName: preview.InviterName, MaskedEmail: preview.MaskedEmail, Status: appservice.InvitationStatus(preview.Status)}, nil
}

// AcceptInvitation 由当前账号接受邀请并加入工作区。
func (o *directOperations) AcceptInvitation(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.InvitationTokenInput) (appservice.Workspace, error) {
	workspace, err := o.acceptInvitation.Execute(ctx, account, input.Token)
	switch {
	case errors.Is(err, invitationaction.ErrInvitationInvalid):
		return appservice.Workspace{}, appservice.InvalidError(meta, i18n.ErrorInvitationInvalid, nil)
	case errors.Is(err, invitationaction.ErrEmailMismatch):
		return appservice.Workspace{}, appservice.InvalidError(meta, i18n.ErrorInvitationEmailMismatch, nil)
	case errors.Is(err, invitationaction.ErrAlreadyMember):
		return appservice.Workspace{}, appservice.ConflictError(meta, i18n.ErrorInvitationAlreadyMember, "invitation_already_member")
	case err != nil:
		if ctx.Err() != nil {
			return appservice.Workspace{}, ctx.Err()
		}
		slog.Warn("接受邀请失败", "account_id", account.Account.ID, "error", err)
		return appservice.Workspace{}, appservice.FailedError(meta, i18n.ErrorInvitationAcceptFailed)
	}
	slog.Info("账号已接受邀请", "account_id", account.Account.ID, "organization_id", workspace.ID)
	return appservice.Workspace{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug, Status: appservice.WorkspaceStatus(workspace.Status)}, nil
}

// invitationError 把邀请管理的 Action 错误转成本地化业务错误。
func (o *directOperations) invitationError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, identity *servermodels.Identity) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		keys := map[common.FieldCode]i18n.Key{
			invitationaction.ValidationEmailInvalid:       i18n.FieldEmailInvalid,
			invitationaction.ValidationEmailMember:        i18n.FieldInvitationEmailMember,
			invitationaction.ValidationEmailPending:       i18n.FieldInvitationEmailPending,
			invitationaction.ValidationDisplayNameInvalid: i18n.FieldDisplayNameInvalid,
			invitationaction.ValidationRoleInvalid:        i18n.FieldMemberRoleInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	if errors.Is(err, invitationaction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorInvitationNotFound)
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateWorkspace, i18n.ErrorWorkspaceUnavailable)
	}
	slog.Warn("成员邀请操作失败", "organization_id", identity.Organization.ID, "failure", failureKey, "error", err)
	return appservice.FailedError(meta, failureKey)
}
