//go:build server

package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// ChannelDetail 定义 Telegram 渠道详情和连接设置。
type ChannelDetail struct {
	channelaction.MessageChannelRecord
	Connection SettingRecord `json:"connection"`
}

// GetChannelQuery 读取当前企业的单个 Telegram 渠道。
type GetChannelQuery struct {
	db *bun.DB
}

// NewGetChannelQuery 创建 Telegram 渠道详情查询。
func NewGetChannelQuery(db *bun.DB) *GetChannelQuery {
	return &GetChannelQuery{db: db}
}

// Execute 返回当前企业的 Telegram 渠道详情。
func (q *GetChannelQuery) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*ChannelDetail, error) {
	return loadChannelDetail(ctx, q.db, identity.Workspace.ID, channelID, false)
}

// loadChannelDetail 读取指定企业中的 Telegram 渠道和设置。
func loadChannelDetail(ctx context.Context, db bun.IDB, workspaceID, channelID string, lock bool) (*ChannelDetail, error) {
	channel := &servermodels.Channel{}
	query := db.NewSelect().
		Model(channel).
		Where("c.id = ?", channelID).
		Where("c.workspace_id = ?", workspaceID).
		Where("c.type = ?", domain.ChannelTypeTelegram)
	if lock {
		query = query.For("UPDATE")
	}
	if err := query.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, channelaction.ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get Telegram channel: %w", err)
	}

	setting := &servermodels.TelegramChannelSetting{}
	settingQuery := db.NewSelect().
		Model(setting).
		Where("tcs.channel_id = ?", channelID).
		Where("tcs.workspace_id = ?", workspaceID)
	if lock {
		settingQuery = settingQuery.For("UPDATE")
	}
	if err := settingQuery.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, channelaction.ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get Telegram channel settings: %w", err)
	}
	// 把 Telegram 设置存储模型转换为传输结构。
	connection := SettingRecord{
		ConnectionMode:     setting.ConnectionMode,
		BotToken:           support.Deref(setting.BotToken),
		BotID:              telegramBotID(channel),
		BotUsername:        setting.BotUsername,
		BotDisplayName:     setting.BotDisplayName,
		WebhookBaseURL:     support.Deref(setting.WebhookBaseURL),
		WebhookSecret:      support.Deref(setting.WebhookSecret),
		WebhookStatus:      setting.WebhookStatus,
		WebhookConnectedAt: setting.WebhookConnectedAt,
	}
	if connection.WebhookBaseURL != "" {
		webhookURL, err := buildWebhookURL(connection.WebhookBaseURL, channelID)
		if err != nil {
			return nil, fmt.Errorf("build Telegram webhook URL: %w", err)
		}
		connection.WebhookURL = webhookURL
	}
	return &ChannelDetail{
		MessageChannelRecord: *channelaction.NewMessageChannelRecord(channel),
		Connection:           connection,
	}, nil
}
