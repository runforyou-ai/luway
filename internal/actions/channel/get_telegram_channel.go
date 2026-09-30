//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TelegramChannelDetail 定义 Telegram 渠道详情和连接设置。
type TelegramChannelDetail struct {
	MessageChannelRecord
	Connection TelegramChannelSettingRecord `json:"connection"`
}

// GetTelegramChannelQuery 读取当前企业的单个 Telegram 渠道。
type GetTelegramChannelQuery struct {
	db *bun.DB
}

// NewGetTelegramChannelQuery 创建 Telegram 渠道详情查询。
func NewGetTelegramChannelQuery(db *bun.DB) *GetTelegramChannelQuery {
	return &GetTelegramChannelQuery{db: db}
}

// Execute 返回当前企业的 Telegram 渠道详情。
func (q *GetTelegramChannelQuery) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*TelegramChannelDetail, error) {
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	return loadTelegramChannelDetail(ctx, q.db, identity.Organization.ID, channelID, false)
}

// loadTelegramChannelDetail 读取指定企业中的 Telegram 渠道和设置。
func loadTelegramChannelDetail(ctx context.Context, db bun.IDB, organizationID, channelID string, lock bool) (*TelegramChannelDetail, error) {
	channel := &servermodels.Channel{}
	query := db.NewSelect().
		Model(channel).
		Where("c.id = ?", channelID).
		Where("c.organization_id = ?", organizationID).
		Where("c.type = ?", domain.ChannelTypeTelegram)
	if lock {
		query = query.For("UPDATE")
	}
	if err := query.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get Telegram channel: %w", err)
	}

	setting := &servermodels.TelegramChannelSetting{}
	settingQuery := db.NewSelect().
		Model(setting).
		Where("tcs.channel_id = ?", channelID).
		Where("tcs.organization_id = ?", organizationID)
	if lock {
		settingQuery = settingQuery.For("UPDATE")
	}
	if err := settingQuery.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get Telegram channel settings: %w", err)
	}
	// 把 Telegram 设置存储模型转换为传输结构。
	connection := TelegramChannelSettingRecord{
		BotToken:           optionalStringValue(setting.BotToken),
		BotID:              setting.BotID,
		BotUsername:        setting.BotUsername,
		BotDisplayName:     setting.BotDisplayName,
		WebhookBaseURL:     optionalStringValue(setting.WebhookBaseURL),
		WebhookSecret:      optionalStringValue(setting.WebhookSecret),
		WebhookStatus:      setting.WebhookStatus,
		WebhookConnectedAt: setting.WebhookConnectedAt,
	}
	if connection.WebhookBaseURL != "" {
		webhookURL, err := telegramWebhookURL(connection.WebhookBaseURL, channelID)
		if err != nil {
			return nil, fmt.Errorf("build Telegram webhook URL: %w", err)
		}
		connection.WebhookURL = webhookURL
	}
	return &TelegramChannelDetail{
		MessageChannelRecord: *messageChannelRecord(channel),
		Connection:           connection,
	}, nil
}
