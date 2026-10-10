//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	channelbindingaction "github.com/runforyou-ai/luway/internal/actions/channelbinding"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
)

const (
	wecomBotInUseReason       = "wecom_bot_in_use"
	channelAccountBoundReason = "channel_account_member_bound"
	channelBindingNotMember   = "channel_binding_not_member"
)

// GetWeComBotChannel 返回企业微信智能机器人渠道详情与连接状态。
func (o *channelOps) GetWeComBotChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.WeComBotChannel, error) {
	detail, err := o.getWeComBotChannel.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.WeComBotChannel{}, channelError(meta, err, i18n.ErrorChannelReadFailed)
	}
	return wecomBotChannelFromRecord(detail), nil
}

// SaveWeComBotChannelConnection 保存企业微信智能机器人的长连接凭据。
func (o *channelOps) SaveWeComBotChannelConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WeComBotChannelConnectionInput) (appservice.WeComBotChannel, error) {
	detail, err := o.saveWeComBotConnection.Execute(ctx, identity, channelID, channelaction.WeComBotConnectionInput{BotID: input.BotID, Secret: input.Secret})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.WeComBotChannel{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, channelFieldKeys))
	}
	if errors.Is(err, channelaction.ErrWeComBotInUse) {
		return appservice.WeComBotChannel{}, appservice.ConflictError(meta, i18n.FieldWeComBotInUse, wecomBotInUseReason)
	}
	if err != nil {
		return appservice.WeComBotChannel{}, channelError(meta, err, i18n.ErrorWeComBotConnectionSaveFailed)
	}
	slog.InfoContext(ctx, "企业微信机器人渠道连接已保存", "channel_id", channelID)
	return wecomBotChannelFromRecord(detail), nil
}

// wecomBotChannelFromRecord 转换企业微信智能机器人渠道详情。
func wecomBotChannelFromRecord(detail *channelaction.WeComBotChannelDetail) appservice.WeComBotChannel {
	var state appservice.ChannelConnectionState
	if connection := detail.Connection; connection != nil {
		status := appservice.ChannelConnectionStatus(connection.Status)
		state = appservice.ChannelConnectionState{Status: &status, ConnectedAt: connection.ConnectedAt, UpdatedAt: &connection.UpdatedAt}
	}
	return appservice.WeComBotChannel{
		MessageChannelSummary: messageChannelFromRecord(&detail.MessageChannelRecord),
		Connection:            appservice.WeComBotChannelConnection{BotID: detail.BotID, Secret: detail.Secret, State: state},
	}
}

// ListChannelAccounts 返回服务员工渠道中的外部账号及其绑定成员。
func (o *channelOps) ListChannelAccounts(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.ChannelAccountList, error) {
	accounts, err := o.listChannelAccounts.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.ChannelAccountList{}, channelAccountError(meta, err, i18n.ErrorChannelAccountListFailed)
	}
	avatarFileIDs := arr.Map(accounts, func(account channelbindingaction.Account) *string { return account.MemberAvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.ChannelAccountList{}, appservice.FailedError(meta, i18n.ErrorChannelAccountListFailed, err)
	}
	result := arr.Map(accounts, func(account channelbindingaction.Account) appservice.ChannelAccount {
		return appservice.ChannelAccount{
			ID: account.ID, ExternalID: account.ExternalID, DisplayName: account.DisplayName,
			MemberIdentityID: account.MemberIdentityID, MemberName: support.Deref(account.MemberName), MemberAvatarURL: optionalFileURL(avatarURLs, account.MemberAvatarFileID),
			LastSeenAt: account.LastSeenAt, CreatedAt: account.CreatedAt,
		}
	})
	return appservice.ChannelAccountList{Accounts: result}, nil
}

// BindChannelAccount 把服务员工渠道中的外部账号绑定或改绑到成员。
func (o *channelOps) BindChannelAccount(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID, accountID string, input appservice.ChannelAccountBindingInput) error {
	if err := o.manageChannelAccounts.Bind(ctx, identity, channelID, accountID, input.MemberIdentityID); err != nil {
		return channelAccountError(meta, err, i18n.ErrorChannelAccountBindFailed)
	}
	slog.InfoContext(ctx, "渠道外部账号已绑定成员", "channel_id", channelID, "channel_identity_id", accountID)
	return nil
}

// UnbindChannelAccount 解除外部账号与成员的绑定。
func (o *channelOps) UnbindChannelAccount(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID, accountID string) error {
	if err := o.manageChannelAccounts.Unbind(ctx, identity, channelID, accountID); err != nil {
		return channelAccountError(meta, err, i18n.ErrorChannelAccountUnbindFailed)
	}
	slog.InfoContext(ctx, "渠道外部账号已解除绑定", "channel_id", channelID, "channel_identity_id", accountID)
	return nil
}

// channelAccountErrors 是外部账号管理的错误转换规则。
var channelAccountErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(channelbindingaction.ErrChannelNotFound, dispatch.NotFound(i18n.ErrorChannelNotFound)),
	dispatch.Is(channelbindingaction.ErrIdentityNotFound, dispatch.NotFound(i18n.ErrorChannelAccountNotFound)),
	dispatch.Is(channelbindingaction.ErrMemberNotFound, dispatch.NotFound(i18n.ErrorChannelAccountMemberInvalid)),
	dispatch.Is(channelbindingaction.ErrMemberBound, dispatch.Conflict(i18n.ErrorChannelAccountMemberBound, channelAccountBoundReason)),
})

// channelAccountError 转换外部账号管理错误。
func channelAccountError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return channelAccountErrors.Translate(meta, err, failureKey)
}

// PreviewChannelBinding 返回绑定链接对应的工作区、渠道与外部账号。
func (o *channelOps) PreviewChannelBinding(ctx context.Context, meta appservice.RequestMeta, input appservice.ChannelBindingTokenInput) (appservice.ChannelBindingPreview, error) {
	preview, err := o.previewChannelBinding.Execute(ctx, input.Token)
	if errors.Is(err, channelbindingaction.ErrLinkInvalid) {
		return appservice.ChannelBindingPreview{}, appservice.NotFoundError(meta, i18n.ErrorChannelBindingInvalid)
	}
	if err != nil {
		return appservice.ChannelBindingPreview{}, appservice.FailedError(meta, i18n.ErrorChannelBindingPreviewFailed, err)
	}
	return appservice.ChannelBindingPreview{
		WorkspaceName: preview.WorkspaceName, WorkspaceSlug: preview.WorkspaceSlug, ChannelName: preview.ChannelName,
		ChannelType: preview.ChannelType, ExternalName: preview.ExternalName, Status: preview.Status,
	}, nil
}

// ConfirmChannelBinding 把绑定链接对应的外部账号绑定到当前账号在该工作区的成员身份。
func (o *channelOps) ConfirmChannelBinding(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ChannelBindingTokenInput) (appservice.ChannelBindingResult, error) {
	slug, err := o.confirmChannelBinding.Execute(ctx, account, input.Token)
	switch {
	case errors.Is(err, channelbindingaction.ErrLinkInvalid), errors.Is(err, channelbindingaction.ErrIdentityBound):
		return appservice.ChannelBindingResult{}, appservice.NotFoundError(meta, i18n.ErrorChannelBindingInvalid)
	case errors.Is(err, channelbindingaction.ErrNotMember):
		return appservice.ChannelBindingResult{}, appservice.ConflictError(meta, i18n.ErrorChannelBindingNotMember, channelBindingNotMember)
	case errors.Is(err, channelbindingaction.ErrMemberBound):
		return appservice.ChannelBindingResult{}, appservice.ConflictError(meta, i18n.ErrorChannelAccountMemberBound, channelAccountBoundReason)
	case err != nil:
		return appservice.ChannelBindingResult{}, appservice.FailedError(meta, i18n.ErrorChannelBindingConfirmFailed, err)
	}
	slog.InfoContext(ctx, "渠道外部账号已由成员确认绑定")
	return appservice.ChannelBindingResult{WorkspaceSlug: slug}, nil
}
