//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// wechatOps 持有微信第三方平台配置、密钥接入与授权接入公众号渠道的 Action 和 Query。
type wechatOps struct {
	wechatPlatformRead             *wechataction.PlatformQuery
	saveWechatPlatform             *wechataction.SavePlatformAction
	checkWechatPlatform            *wechataction.CheckPlatformAction
	wechatKeyChannelRead           *wechataction.KeyChannelQuery
	saveWechatKeyConnection        *wechataction.SaveKeyConnectionAction
	checkWechatKeyConnection       *wechataction.CheckKeyConnectionAction
	wechatAuthorizationChannelRead *wechataction.AuthorizationChannelQuery
	startWechatAuthorization       *wechataction.StartAuthorizationAction
	checkWechatAuthorization       *wechataction.CheckAuthorizationAction
}

// newWechatOps 创建微信第三方平台与公众号渠道的业务实现依赖，publicURL 返回生成接入地址的部署地址。
func newWechatOps(db *bun.DB, client *wechat.Client, publicURL func() string) *wechatOps {
	return &wechatOps{
		wechatPlatformRead:             wechataction.NewPlatformQuery(db, publicURL),
		saveWechatPlatform:             wechataction.NewSavePlatformAction(db, client),
		checkWechatPlatform:            wechataction.NewCheckPlatformAction(db, client),
		wechatKeyChannelRead:           wechataction.NewKeyChannelQuery(db, publicURL),
		saveWechatKeyConnection:        wechataction.NewSaveKeyConnectionAction(db, client, publicURL),
		checkWechatKeyConnection:       wechataction.NewCheckKeyConnectionAction(db, client, publicURL),
		wechatAuthorizationChannelRead: wechataction.NewAuthorizationChannelQuery(db),
		startWechatAuthorization:       wechataction.NewStartAuthorizationAction(db, client, publicURL),
		checkWechatAuthorization:       wechataction.NewCheckAuthorizationAction(db, client),
	}
}

// GetWechatPlatform 返回微信第三方平台配置、接入地址、各服务器出口 IP 与平台凭据状态。
func (o *wechatOps) GetWechatPlatform(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.WechatPlatform, error) {
	return o.readWechatPlatform(ctx, meta)
}

// SaveWechatPlatform 保存微信第三方平台凭据，已收到验证票据时立即获取平台凭据。
func (o *wechatOps) SaveWechatPlatform(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.WechatPlatformInput) (appservice.WechatPlatform, error) {
	if err := o.saveWechatPlatform.Execute(ctx, account, wechataction.PlatformInput{
		ComponentAppID: input.ComponentAppID, ComponentAppSecret: input.ComponentAppSecret,
		Token: input.Token, EncodingAESKey: input.EncodingAESKey,
	}); err != nil {
		return appservice.WechatPlatform{}, wechatPlatformError(meta, err, i18n.ErrorWechatPlatformSaveFailed)
	}
	slog.InfoContext(ctx, "微信开放平台配置已保存", "component_app_id", input.ComponentAppID)
	return o.readWechatPlatform(ctx, meta)
}

// CheckWechatPlatform 立即重新获取微信平台凭据并返回结果。
func (o *wechatOps) CheckWechatPlatform(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.WechatPlatform, error) {
	if err := o.checkWechatPlatform.Execute(ctx); err != nil {
		return appservice.WechatPlatform{}, wechatPlatformError(meta, err, i18n.ErrorWechatPlatformCheckFailed)
	}
	return o.readWechatPlatform(ctx, meta)
}

// readWechatPlatform 读取微信第三方平台并转换为应用契约。
func (o *wechatOps) readWechatPlatform(ctx context.Context, meta appservice.RequestMeta) (appservice.WechatPlatform, error) {
	platform, err := o.wechatPlatformRead.Execute(ctx)
	if err != nil {
		return appservice.WechatPlatform{}, wechatPlatformError(meta, err, i18n.ErrorWechatPlatformReadFailed)
	}
	output := appservice.WechatPlatform{
		Configured: platform.Configured, ComponentAppID: platform.ComponentAppID, ComponentAppSecret: platform.ComponentAppSecret,
		Token: platform.Token, EncodingAESKey: platform.EncodingAESKey,
		AuthorizationDomain: platform.AuthorizationDomain, EventURL: platform.EventURL, MessageURL: platform.MessageURL,
		Servers:                wechatServersFromRecord(platform.Servers),
		VerifyTicketReceivedAt: platform.VerifyTicketReceivedAt, AccessTokenExpiresAt: platform.AccessTokenExpiresAt,
		TokenFailedAt: platform.TokenFailedAt, TokenFailureDetail: platform.TokenFailureDetail,
		Status: support.NilIfZero(platform.Status),
	}
	if platform.TokenFailure != nil {
		output.TokenFailure = new(*platform.TokenFailure)
	}
	return output, nil
}

// wechatPlatformErrors 是微信第三方平台的错误转换规则，其余错误按平台管理错误处理。
var wechatPlatformErrors = dispatch.Catalogs(dispatch.Catalog{
	dispatch.FieldRule(wechatFieldKeys),
	dispatch.Is(wechataction.ErrPlatformNotConfigured, dispatch.Invalid(i18n.ErrorWechatPlatformNotConfigured)),
}, platformErrors)

// wechatPlatformError 把微信第三方平台的校验与配置错误转换为本地化错误，其余错误按平台管理错误处理。
func wechatPlatformError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return wechatPlatformErrors.Translate(meta, err, failureKey)
}

// wechatFieldKeys 把微信凭据校验错误码映射为本地化文案键。
var wechatFieldKeys = map[common.FieldCode]i18n.Key{
	wechataction.ValidationAppIDInvalid:          i18n.FieldWechatAppIDInvalid,
	wechataction.ValidationAppIDImmutable:        i18n.FieldWechatAppIDImmutable,
	wechataction.ValidationEncodingAESKeyInvalid: i18n.FieldWechatEncodingAESKeyInvalid,
}

// GetWechatKeyChannel 返回密钥接入公众号渠道详情、连接与接口调用凭据状态。
func (o *wechatOps) GetWechatKeyChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.WechatKeyChannel, error) {
	channel, err := o.wechatKeyChannelRead.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.WechatKeyChannel{}, wechatChannelError(meta, err, i18n.ErrorChannelReadFailed)
	}
	return wechatKeyChannelFromRecord(channel), nil
}

// SaveWechatKeyChannelConnection 连接公众号或更新密钥接入凭据。
func (o *wechatOps) SaveWechatKeyChannelConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WechatKeyConnectionInput) (appservice.WechatKeyChannel, error) {
	channel, err := o.saveWechatKeyConnection.Execute(ctx, identity, channelID, wechataction.KeyConnectionInput{
		AppID: input.AppID, AppSecret: input.AppSecret, Token: input.Token,
		EncryptionMode: input.EncryptionMode, EncodingAESKey: input.EncodingAESKey,
	})
	if err != nil {
		return appservice.WechatKeyChannel{}, wechatChannelError(meta, err, i18n.ErrorWechatConnectionSaveFailed)
	}
	slog.InfoContext(ctx, "公众号密钥接入已保存", "channel_id", channelID, "app_id", channel.Connection.AppID)
	return wechatKeyChannelFromRecord(channel), nil
}

// CheckWechatKeyChannelConnection 立即重新获取密钥接入公众号的接口调用凭据并返回渠道详情。
func (o *wechatOps) CheckWechatKeyChannelConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.WechatKeyChannel, error) {
	channel, err := o.checkWechatKeyConnection.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.WechatKeyChannel{}, wechatChannelError(meta, err, i18n.ErrorWechatConnectionCheckFailed)
	}
	return wechatKeyChannelFromRecord(channel), nil
}

// GetWechatAuthorizationChannel 返回授权接入公众号渠道详情、授权状态与接口调用凭据状态。
func (o *wechatOps) GetWechatAuthorizationChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.WechatAuthorizationChannel, error) {
	channel, err := o.wechatAuthorizationChannelRead.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.WechatAuthorizationChannel{}, wechatChannelError(meta, err, i18n.ErrorChannelReadFailed)
	}
	return wechatAuthorizationChannelFromRecord(channel), nil
}

// StartWechatAuthorization 为授权接入渠道发起公众号授权，返回授权发起页地址。
func (o *wechatOps) StartWechatAuthorization(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.WechatAuthorizationStart, error) {
	url, err := o.startWechatAuthorization.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.WechatAuthorizationStart{}, wechatChannelError(meta, err, i18n.ErrorWechatAuthorizationStartFailed)
	}
	return appservice.WechatAuthorizationStart{URL: url}, nil
}

// CheckWechatAuthorizationChannelConnection 立即重新获取授权接入公众号的接口调用凭据并返回渠道详情。
func (o *wechatOps) CheckWechatAuthorizationChannelConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.WechatAuthorizationChannel, error) {
	channel, err := o.checkWechatAuthorization.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.WechatAuthorizationChannel{}, wechatChannelError(meta, err, i18n.ErrorWechatConnectionCheckFailed)
	}
	return wechatAuthorizationChannelFromRecord(channel), nil
}

// wechatChannelErrors 是公众号渠道连接的错误转换规则，其余错误按渠道错误处理。
var wechatChannelErrors = dispatch.Catalogs(dispatch.Catalog{
	dispatch.FieldRule(wechatFieldKeys),
	// 按凭据验证失败原因给出带调用方 IP 或微信错误说明的提示。
	func(meta appservice.RequestMeta, err error) error {
		credentialError, ok := errors.AsType[*wechataction.CredentialError](err)
		if !ok {
			return nil
		}
		switch credentialError.Failure {
		case domain.WechatTokenFailureIPNotWhitelisted:
			invalidError := appservice.InvalidError(meta, i18n.ErrorWechatIPNotWhitelisted, nil)
			invalidError.Message = i18n.LocalizeTemplate(string(meta.Locale), i18n.ErrorWechatIPNotWhitelisted, map[string]any{"IP": credentialError.Detail})
			return invalidError
		case domain.WechatTokenFailureRejected:
			invalidError := appservice.InvalidError(meta, i18n.ErrorWechatCredentialRejected, nil)
			invalidError.Message = i18n.LocalizeTemplate(string(meta.Locale), i18n.ErrorWechatCredentialRejected, map[string]any{"Detail": credentialError.Detail})
			return invalidError
		default:
			return appservice.UnavailableError(meta, i18n.ErrorWechatUnavailable, nil)
		}
	},
	dispatch.Is(wechataction.ErrAppIDTaken, dispatch.Conflict(i18n.ErrorWechatAppIDTaken, "wechat_app_id_taken")),
	dispatch.Is(channelaction.ErrWechatAccountEnabledElsewhere, dispatch.Conflict(i18n.ErrorWechatAccountEnabledElsewhere, "wechat_account_enabled_elsewhere")),
	dispatch.Is(wechataction.ErrAccountNotConnected, dispatch.Conflict(i18n.ErrorWechatChannelNotConnected, "wechat_channel_not_connected")),
	dispatch.Is(wechataction.ErrNotAuthorized, dispatch.Conflict(i18n.ErrorWechatChannelNotAuthorized, "wechat_channel_not_authorized")),
	dispatch.Is(wechataction.ErrPlatformNotConfigured, dispatch.Conflict(i18n.ErrorWechatPlatformNotConfigured, "wechat_platform_not_configured")),
	dispatch.Is(wechataction.ErrPlatformUnavailable, dispatch.Unavailable(i18n.ErrorWechatPlatformUnavailable)),
	dispatch.Is(wechat.ErrUnavailable, dispatch.Unavailable(i18n.ErrorWechatUnavailable)),
}, channelErrors)

// wechatChannelError 把公众号渠道连接的校验、凭据、授权与归属错误转换为本地化错误，其余错误按渠道错误处理。
func wechatChannelError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return wechatChannelErrors.Translate(meta, err, failureKey)
}

// wechatTokenStateFromRecord 把接口调用凭据状态转换为应用契约。
func wechatTokenStateFromRecord(state wechataction.TokenState) appservice.WechatTokenState {
	output := appservice.WechatTokenState{
		AccessTokenExpiresAt: state.AccessTokenExpiresAt, TokenFailedAt: state.TokenFailedAt, TokenFailureDetail: state.TokenFailureDetail,
	}
	if state.TokenFailure != nil {
		output.TokenFailure = new(*state.TokenFailure)
	}
	return output
}

// wechatKeyChannelFromRecord 把密钥接入公众号渠道详情转换为应用契约。
func wechatKeyChannelFromRecord(channel *wechataction.KeyChannel) appservice.WechatKeyChannel {
	connection := channel.Connection
	output := appservice.WechatKeyConnection{
		AppID: connection.AppID, AppSecret: connection.AppSecret, Token: connection.Token, EncodingAESKey: connection.EncodingAESKey,
		ServerURL: connection.ServerURL, ServerVerifiedAt: connection.ServerVerifiedAt,
		Servers:          wechatServersFromRecord(connection.Servers),
		WechatTokenState: wechatTokenStateFromRecord(connection.TokenState),
	}
	if connection.EncryptionMode != nil {
		output.EncryptionMode = new(*connection.EncryptionMode)
	}
	return appservice.WechatKeyChannel{MessageChannelSummary: messageChannelFromRecord(&channel.MessageChannelRecord), Connection: output}
}

// wechatAuthorizationChannelFromRecord 把授权接入公众号渠道详情转换为应用契约。
func wechatAuthorizationChannelFromRecord(channel *wechataction.AuthorizationChannel) appservice.WechatAuthorizationChannel {
	connection := channel.Connection
	output := appservice.WechatAuthorizationConnection{
		PlatformConfigured: connection.PlatformConfigured, AppID: connection.AppID,
		NickName: connection.NickName, HeadImageURL: connection.HeadImageURL, PrincipalName: connection.PrincipalName, UserName: connection.UserName,
		MissingPermissions: make([]appservice.WechatPermission, 0, len(connection.MissingPermissions)),
		AuthorizedAt:       connection.AuthorizedAt, RevokedAt: connection.RevokedAt,
		WechatTokenState: wechatTokenStateFromRecord(connection.TokenState),
	}
	if connection.Status != nil {
		output.Status = new(*connection.Status)
	}
	output.MissingPermissions = append(output.MissingPermissions, connection.MissingPermissions...)
	return appservice.WechatAuthorizationChannel{MessageChannelSummary: messageChannelFromRecord(&channel.MessageChannelRecord), Connection: output}
}

// wechatServersFromRecord 转换各服务器的出口 IP。
func wechatServersFromRecord(servers []wechataction.ServerEgress) []appservice.WechatServer {
	return arr.OrEmpty(arr.Map(servers, func(server wechataction.ServerEgress) appservice.WechatServer {
		return appservice.WechatServer{Hostname: server.Hostname, EgressIP: server.EgressIP, Online: server.Online}
	}))
}
