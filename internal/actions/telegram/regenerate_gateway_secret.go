//go:build server

package telegram

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

// RegenerateGatewaySecretAction 为业务系统转发接入的 Telegram 渠道生成新的转发密钥。
type RegenerateGatewaySecretAction struct {
	db *bun.DB
}

// NewRegenerateGatewaySecretAction 创建 Telegram 转发密钥重新生成操作。
func NewRegenerateGatewaySecretAction(db *bun.DB) *RegenerateGatewaySecretAction {
	return &RegenerateGatewaySecretAction{db: db}
}

// Execute 替换转发密钥，旧密钥立即失效；渠道启用时连接状态回到等待转发。
func (a *RegenerateGatewaySecretAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*ChannelDetail, error) {
	secret := random.Hex(32)
	var detail *ChannelDetail
	err := serverstorage.WithSessionLock(ctx, a.db, serverstorage.LockTelegramChannel, []string{channelID}, func(conn bun.Conn) error {
		return realtime.RunInTx(ctx, conn, func(ctx context.Context, tx bun.Tx) error {
			if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
				return err
			}
			current, err := loadChannelDetail(ctx, tx, identity.Workspace.ID, channelID, true)
			if err != nil {
				return err
			}
			if current.Connection.ConnectionMode != string(domain.TelegramConnectionGateway) {
				return ErrGatewayModeRequired
			}
			var status *domain.TelegramWebhookStatus
			if current.Enabled {
				status = new(domain.TelegramWebhookStatusWaiting)
			}
			if _, err := tx.NewUpdate().
				Model((*servermodels.TelegramChannelSetting)(nil)).
				Set("webhook_secret = ?", secret).
				Set("webhook_status = ?", status).
				Set("webhook_connected_at = NULL").
				Where("channel_id = ? AND workspace_id = ?", channelID, identity.Workspace.ID).
				Exec(ctx); err != nil {
				return fmt.Errorf("regenerate Telegram gateway secret: %w", err)
			}
			detail, err = loadChannelDetail(ctx, tx, identity.Workspace.ID, channelID, false)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}
