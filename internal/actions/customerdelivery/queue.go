//go:build server

// Package customerdelivery 管理客户消息的持久有序外发。
package customerdelivery

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/domain"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

const (
	SendActionName = "customer_delivery.send"
	ScanActionName = "customer_delivery.scan"
)

var (
	ErrUnavailable = errors.New("customer delivery unavailable")
	ErrConflict    = errors.New("customer delivery state conflict")
)

// Route 保存本次发送所属的渠道身份与机器人。
type Route struct {
	ChannelID              string             `bun:"channel_id"`
	IdentityID             string             `bun:"identity_id"`
	ChannelType            domain.ChannelType `bun:"channel_type"`
	Enabled                bool               `bun:"enabled"`
	BotID                  *int64             `bun:"bot_id"`
	ReplyProviderMessageID *string
}

// Prepare 读取外发目标，Telegram 在客服周期之前对渠道取共享锁并锁定渠道身份。
func Prepare(ctx context.Context, db bun.IDB, organizationID, conversationID string) (Route, error) {
	var route Route
	query := db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("cci.id AS identity_id, ch.id AS channel_id, ch.type AS channel_type, ch.enabled, tcs.bot_id").
		Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
		Join("JOIN channels AS ch ON ch.id = cci.channel_id AND ch.organization_id = cci.organization_id").
		Join("LEFT JOIN telegram_channel_settings AS tcs ON tcs.channel_id = ch.id AND tcs.organization_id = ch.organization_id").
		Where("cc.organization_id = ? AND cc.conversation_id = ?", organizationID, conversationID)
	err := query.Scan(ctx, &route)
	if errors.Is(err, sql.ErrNoRows) {
		return route, ErrUnavailable
	}
	if err != nil {
		return route, err
	}
	if route.ChannelType == domain.ChannelTypeTelegram {
		// 持有渠道共享锁直至入队完成。
		if err := query.For("SHARE OF ch").Scan(ctx, &route); err != nil {
			return route, err
		}
		_, err = db.ExecContext(ctx, "SELECT id FROM contact_channel_identities WHERE id = ? AND organization_id = ? FOR UPDATE", route.IdentityID, organizationID)
	}
	return route, err
}

// Input 定义单条投递的快速唤醒参数。
type Input struct {
	DeliveryID string `json:"deliveryId"`
}

// Enqueue 在业务事务中保存发送顺序和投递唤醒。
func Enqueue(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, route Route, message *models.Message) error {
	var position int64
	if err := db.NewSelect().TableExpr("customer_message_deliveries").ColumnExpr("COALESCE(MAX(position), 0) + 1").
		Where("organization_id = ? AND channel_id = ? AND contact_channel_identity_id = ?", message.OrganizationID, route.ChannelID, route.IdentityID).Scan(ctx, &position); err != nil {
		return err
	}
	now := time.Now().UTC()
	delivery := &models.CustomerMessageDelivery{
		ID: uuid.NewV7().String(), OrganizationID: message.OrganizationID, ConversationID: message.ConversationID,
		MessageID: message.ID, ChannelID: route.ChannelID, ContactChannelIdentityID: route.IdentityID,
		BotID: *route.BotID, ReplyProviderMessageID: route.ReplyProviderMessageID, Position: position, Status: domain.CustomerDeliveryPending,
		AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := db.NewInsert().Model(delivery).Exec(ctx); err != nil {
		return err
	}
	if enqueuer != nil {
		_, err := enqueuer.EnqueueIn(ctx, db, SendActionName, Input{DeliveryID: delivery.ID}, servertask.EnqueueOptions{Queue: servertask.QueueDelivery, IdempotencyKey: "cdeliv-item:" + delivery.ID})
		return err
	}
	return nil
}
