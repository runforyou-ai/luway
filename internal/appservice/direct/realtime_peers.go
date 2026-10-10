//go:build server

package direct

import (
	"context"
	"errors"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/actions/realtimeauth"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/support/random"
)

// SetRealtimePrefix 设置部署的实时主题前缀。
func (b *WebsiteVisitorBackend) SetRealtimePrefix(prefix string) { b.realtimePrefix = prefix }

// GetVisitorRealtimeConnection 以已认证访客身份签发短期实时凭据。
func (b *WebsiteVisitorBackend) GetVisitorRealtimeConnection(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID string) (_ appservice.RealtimePeerConnection, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.GetVisitorRealtimeConnection")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	target, err := b.AuthenticateVisitor(ctx, meta, channelID, externalID)
	if err != nil {
		return appservice.RealtimePeerConnection{}, err
	}
	identity, err := realtimeauth.Visitor(ctx, b.db, target.ChannelIdentityID)
	if err != nil {
		return appservice.RealtimePeerConnection{}, err
	}
	token, err := realtimeauth.Issue(ctx, b.db, "v", identity, meta.CustomerToken)
	channel := realtime.VisitorChannel(identity.WorkspaceID, identity.ID)
	return appservice.RealtimePeerConnection{Token: token, UserID: "v_" + identity.ID, Channel: channel, TypingChannel: channel + ".typing", ReceptionChannel: "w." + identity.WorkspaceID + ".visitors", Prefix: b.realtimePrefix, Path: "/nats"}, err
}

// GetComputerRealtimeConnection 以当前电脑凭据签发短期工作通知授权。
func (o *computerOps) GetComputerRealtimeConnection(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity) (appservice.RealtimePeerConnection, error) {
	identity, err := realtimeauth.Computer(ctx, o.db, computer.ComputerID)
	if errors.Is(err, realtimeauth.ErrInvalid) {
		return appservice.RealtimePeerConnection{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorComputerCredentialInvalid)
	}
	if err != nil {
		return appservice.RealtimePeerConnection{}, computerRequestError(meta, err)
	}
	if identity.Secret != random.HashToken(meta.Token) {
		return appservice.RealtimePeerConnection{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorComputerCredentialInvalid)
	}
	token, err := realtimeauth.Issue(ctx, o.db, "c", identity, "")
	if err != nil {
		return appservice.RealtimePeerConnection{}, computerRequestError(meta, err)
	}
	return appservice.RealtimePeerConnection{Token: token, UserID: "c_" + identity.ID, Channel: realtime.ComputerChannel(identity.WorkspaceID, identity.ID), Prefix: o.realtimePrefix, Path: "/nats"}, nil
}

// HeartbeatComputer 按数据库时钟记录已认证电脑的最近在线时间。
func (o *computerOps) HeartbeatComputer(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity) error {
	err := o.touchComputer.Execute(ctx, computer, meta.Token)
	if errors.Is(err, computeraction.ErrCredentialInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorComputerCredentialInvalid)
	}
	if err != nil {
		return computerRequestError(meta, err)
	}
	return nil
}
