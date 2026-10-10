//go:build server

// Package channeldelivery 管理渠道消息经外部平台的持久有序外发。
package channeldelivery

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// AdvanceActionName 是推进一个渠道身份投递管道的任务。
const AdvanceActionName = "channel_delivery.advance"

// unfinishedCondition 是投递仍占据渠道身份管道的状态条件。
const unfinishedCondition = "status IN ('pending','retry_wait','sending','uncertain')"

var (
	// ErrUnavailable 表示会话没有渠道外发目标，或指定投递不存在。
	ErrUnavailable = errors.New("customer delivery unavailable")
	// ErrConflict 表示投递当前状态不允许请求的人工处理。
	ErrConflict = errors.New("customer delivery state conflict")
)

// Route 保存本次发送所属的渠道身份与平台账号。
type Route struct {
	ChannelID         string             `bun:"channel_id"`
	IdentityID        string             `bun:"identity_id"`
	ChannelType       domain.ChannelType `bun:"channel_type"`
	Enabled           bool               `bun:"enabled"`
	ProviderAccountID *string            `bun:"provider_account_id"`
	// RecipientBound 表示对方仍是会话发起人：服务员工的渠道要求渠道身份当前绑定的成员就是会话的发起成员，其余渠道恒为真。
	RecipientBound bool `bun:"recipient_bound"`
	// ReplyWindowOpen 表示还能向对方发送消息：受回复窗口限制的渠道要求渠道身份有可用的回复窗口，其余渠道恒为真。
	ReplyWindowOpen        bool `bun:"-"`
	ReplyProviderMessageID *string
}

// Platform 判断渠道消息经外部平台投递。
func (r Route) Platform() bool {
	return domain.ChannelCapabilitiesOf(r.ChannelType).ViaPlatform()
}

// Ready 判断渠道已启用、连接了可发送的平台账号，对方仍是会话发起人且回复窗口可用。
func (r Route) Ready() bool {
	return r.Enabled && r.ProviderAccountID != nil && r.RecipientBound && r.ReplyWindowOpen
}

// RecipientBoundCondition 是以 svc、ci 为别名时对方仍是会话发起人的条件：服务员工的渠道要求渠道身份当前绑定的成员就是会话的发起成员。
const RecipientBoundCondition = `svc.audience <> 'employee' OR EXISTS (SELECT 1 FROM chat_subjects AS requester_cs WHERE requester_cs.workspace_id = svc.workspace_id AND requester_cs.id = svc.requester_subject_id AND requester_cs.kind = 'workspace_identity' AND requester_cs.source_id = ci.user_identity_id)`

// Prepare 读取外发目标，经外部平台投递的渠道在客服周期之前对渠道取共享锁并锁定渠道身份，受回复窗口限制的渠道在锁定渠道身份后判断回复窗口是否可用。
func Prepare(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (Route, error) {
	var route Route
	query := db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("ci.id AS identity_id, ch.id AS channel_id, ch.type AS channel_type, ch.enabled, ch.provider_account_id").
		ColumnExpr("("+RecipientBoundCondition+") AS recipient_bound").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = cc.workspace_id AND svc.conversation_id = cc.conversation_id").
		Join("JOIN channels AS ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id").
		Where("cc.workspace_id = ? AND cc.conversation_id = ?", workspaceID, conversationID)
	err := query.Scan(ctx, &route)
	if errors.Is(err, sql.ErrNoRows) {
		return route, ErrUnavailable
	}
	if err != nil {
		return route, err
	}
	if route.Platform() {
		// 持有渠道共享锁直至入队完成。
		if err := query.For("SHARE OF ch").Scan(ctx, &route); err != nil {
			return route, err
		}
		if _, err := db.ExecContext(ctx, "SELECT id FROM channel_identities WHERE id = ? AND workspace_id = ? FOR UPDATE", route.IdentityID, workspaceID); err != nil {
			return route, err
		}
	}
	route.ReplyWindowOpen = true
	if domain.ChannelCapabilitiesOf(route.ChannelType).ReplyWindow {
		route.ReplyWindowOpen, err = db.NewSelect().TableExpr("channel_reply_windows AS crw").
			Where("crw.workspace_id = ? AND crw.channel_identity_id = ?", workspaceID, route.IdentityID).
			Where("?", UsableReplyWindowCondition("crw", 1)).
			Exists(ctx)
	}
	return route, err
}

// AdvanceInput 指定要推进投递管道的渠道身份。
type AdvanceInput struct {
	WorkspaceID       string `json:"workspaceId"`
	ChannelIdentityID string `json:"channelIdentityId"`
	// Scheduled 表示按到期时刻排的推进：只处理到期状态，队头可以发送时排一次立即推进，不调用平台。
	Scheduled bool `json:"scheduled,omitempty"`
}

// pipeline 是一个渠道身份的投递管道。
type pipeline struct {
	WorkspaceID string             `bun:"workspace_id"`
	ChannelID   string             `bun:"channel_id"`
	ChannelType domain.ChannelType `bun:"channel_type"`
	IdentityID  string             `bun:"identity_id"`
}

// advanceRequest 返回推进渠道身份管道的任务请求：at 为零时立即执行且不去重，经长连接收发的渠道按渠道长连接路由，由持有连接的服务端实例执行；
// 否则是在 at 执行、按渠道身份与时刻去重的定时推进，不按连接路由。
func advanceRequest(ctx context.Context, db bun.IDB, p pipeline, at time.Time) (servertask.EnqueueRequest, error) {
	options := servertask.EnqueueOptions{WorkspaceID: p.WorkspaceID, Queue: servertask.QueueDelivery}
	if at.IsZero() && domain.ChannelCapabilitiesOf(p.ChannelType).Connection {
		options.Route = channeladapter.ConnectionRoute(p.ChannelID)
	}
	if !at.IsZero() {
		// 延迟按任务运行时写入可执行时间所用的事务时刻计算，使可执行时间恰为 at。
		now, err := serverstorage.Now(ctx, db)
		if err != nil {
			return servertask.EnqueueRequest{}, err
		}
		options.Delay = at.Sub(now)
		options.IdempotencyKey = "chdeliv:" + p.IdentityID + ":" + at.UTC().Format(time.RFC3339Nano)
	}
	return servertask.EnqueueRequest{ActionName: AdvanceActionName, Payload: AdvanceInput{WorkspaceID: p.WorkspaceID, ChannelIdentityID: p.IdentityID, Scheduled: !at.IsZero()}, Options: options}, nil
}

// enqueueAdvance 在调用方事务中为渠道身份管道排一次推进，at 的含义同 advanceRequest。
func enqueueAdvance(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, p pipeline, at time.Time) error {
	request, err := advanceRequest(ctx, db, p, at)
	if err != nil {
		return err
	}
	_, err = enqueuer.EnqueueIn(ctx, request.ActionName, request.Payload, request.Options)
	return err
}

// WakeChannel 在调用方事务中为渠道下有未完成投递的渠道身份各排一次立即推进。
func WakeChannel(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, channelID string) error {
	var pipelines []pipeline
	if err := db.NewSelect().TableExpr("channel_message_deliveries AS d").
		ColumnExpr("DISTINCT d.workspace_id, d.channel_id, ch.type AS channel_type, d.channel_identity_id AS identity_id").
		Join("JOIN channels AS ch ON ch.id = d.channel_id AND ch.workspace_id = d.workspace_id").
		Where("d.workspace_id = ? AND d.channel_id = ?", workspaceID, channelID).Where("d."+unfinishedCondition).
		Scan(ctx, &pipelines); err != nil {
		return err
	}
	requests := make([]servertask.EnqueueRequest, len(pipelines))
	for index, p := range pipelines {
		var err error
		if requests[index], err = advanceRequest(ctx, db, p, time.Time{}); err != nil {
			return err
		}
	}
	err := enqueuer.EnqueueManyIn(ctx, requests)
	return err
}

// Enqueue 在业务事务中保存发送顺序并排一次渠道身份管道的推进。
func Enqueue(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, route Route, message *models.Message) error {
	var position int64
	if err := db.NewSelect().TableExpr("channel_message_deliveries").ColumnExpr("COALESCE(MAX(position), 0) + 1").
		Where("workspace_id = ? AND channel_id = ? AND channel_identity_id = ?", message.WorkspaceID, route.ChannelID, route.IdentityID).Scan(ctx, &position); err != nil {
		return err
	}
	delivery := &models.ChannelMessageDelivery{
		ID: uuid.NewV7().String(), WorkspaceID: message.WorkspaceID, ConversationID: message.ConversationID,
		MessageID: message.ID, ChannelID: route.ChannelID, ChannelIdentityID: route.IdentityID,
		ProviderAccountID: *route.ProviderAccountID, ReplyProviderMessageID: route.ReplyProviderMessageID, Position: position, Status: domain.ChannelDeliveryPending,
	}
	if _, err := db.NewInsert().Model(delivery).
		Value("available_at", "now()").Value("created_at", "now()").Value("updated_at", "now()").
		Returning("available_at, created_at, updated_at").Exec(ctx); err != nil {
		return err
	}
	if enqueuer != nil {
		return enqueueAdvance(ctx, db, enqueuer, pipeline{WorkspaceID: delivery.WorkspaceID, ChannelID: delivery.ChannelID, ChannelType: route.ChannelType, IdentityID: delivery.ChannelIdentityID}, time.Time{})
	}
	return nil
}
