//go:build server

package wechat

import (
	"context"
	"crypto/rand"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// StartAuthorizationAction 为授权接入渠道发起公众号授权。
type StartAuthorizationAction struct {
	db        *bun.DB
	client    *wechat.Client
	publicURL func() string
}

// NewStartAuthorizationAction 创建发起授权操作，publicURL 返回生成授权发起页地址的部署地址。
func NewStartAuthorizationAction(db *bun.DB, client *wechat.Client, publicURL func() string) *StartAuthorizationAction {
	return &StartAuthorizationAction{db: db, client: client, publicURL: publicURL}
}

// Execute 获取预授权码并覆盖渠道的授权意图，返回授权发起页地址。部署尚未配置第三方平台时返回 ErrPlatformNotConfigured，平台凭据不可用时返回 ErrPlatformUnavailable。
func (a *StartAuthorizationAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (string, error) {
	if _, err := loadChannelModel(ctx, a.db.NewSelect(), domain.ChannelTypeWechatAuthorization, identity.Workspace.ID, channelID); err != nil {
		return "", err
	}
	platform, err := loadPlatform(ctx, a.db.NewSelect())
	if err != nil {
		return "", err
	}
	componentToken, err := PlatformAccessToken(ctx, a.db, a.client, false)
	if err != nil {
		return "", platformError(err)
	}
	code, err := a.client.CreatePreAuthCode(ctx, componentToken, platform.ComponentAppID)
	if err != nil {
		return "", platformError(err)
	}
	state := rand.Text()
	err = serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := loadChannelModel(ctx, tx.NewSelect().For("UPDATE OF c"), domain.ChannelTypeWechatAuthorization, identity.Workspace.ID, channelID); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&servermodels.WechatAuthorizationIntent{
			ChannelID: channelID, WorkspaceID: identity.Workspace.ID, State: state, PreAuthCode: code.Value,
		}).
			Value("expires_at", "now() + make_interval(secs => ?)", code.ExpiresIn.Seconds()).
			On("CONFLICT (channel_id) DO UPDATE").
			Set("created_at = now()").
			Set("state = EXCLUDED.state").
			Set("pre_auth_code = EXCLUDED.pre_auth_code").
			Set("expires_at = EXCLUDED.expires_at").
			Set("completed_at = NULL").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("save wechat authorization intent: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return a.publicURL() + AuthorizationPagePath + state, nil
}
