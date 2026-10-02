//go:build server

package channel

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/channelstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RegenerateTelegramGatewaySecretAction 为业务系统转发接入的 Telegram 渠道生成新的转发密钥。
type RegenerateTelegramGatewaySecretAction struct {
	db *bun.DB
}

// NewRegenerateTelegramGatewaySecretAction 创建 Telegram 转发密钥重新生成操作。
func NewRegenerateTelegramGatewaySecretAction(db *bun.DB) *RegenerateTelegramGatewaySecretAction {
	return &RegenerateTelegramGatewaySecretAction{db: db}
}

// Execute 替换转发密钥，旧密钥立即失效；渠道启用时连接状态回到等待转发。
func (a *RegenerateTelegramGatewaySecretAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*TelegramChannelDetail, error) {
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	secret, err := newTelegramWebhookSecret()
	if err != nil {
		return nil, fmt.Errorf("generate Telegram gateway secret: %w", err)
	}
	var detail *TelegramChannelDetail
	err = channelstate.WithTelegramLock(ctx, a.db, channelID, func(conn bun.Conn) error {
		return realtime.RunInTx(ctx, conn, func(ctx context.Context, tx bun.Tx) error {
			if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
				return err
			}
			current, err := loadTelegramChannelDetail(ctx, tx, identity.Organization.ID, channelID, true)
			if err != nil {
				return err
			}
			if current.Connection.ConnectionMode != string(domain.TelegramConnectionGateway) {
				return ErrTelegramGatewayModeRequired
			}
			var status *domain.TelegramWebhookStatus
			if current.Enabled {
				waiting := domain.TelegramWebhookStatusWaiting
				status = &waiting
			}
			if _, err := tx.NewUpdate().
				Model((*servermodels.TelegramChannelSetting)(nil)).
				Set("webhook_secret = ?", secret).
				Set("webhook_status = ?", status).
				Set("webhook_connected_at = NULL").
				Set("updated_at = now()").
				Where("channel_id = ? AND organization_id = ?", channelID, identity.Organization.ID).
				Exec(ctx); err != nil {
				return fmt.Errorf("regenerate Telegram gateway secret: %w", err)
			}
			detail, err = loadTelegramChannelDetail(ctx, tx, identity.Organization.ID, channelID, false)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}
