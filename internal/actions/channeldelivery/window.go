//go:build server

package channeldelivery

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// OpenReplyWindow 在调用方事务中为渠道身份开启适配器给出的新窗口；同一触发动作已有开启时间不早于它的窗口时不做改动，返回是否开启了新窗口。
//
// 开启前锁定渠道身份，使同一身份的开启串行执行；新窗口插入后删除被它取代且没有预占的窗口，仍有预占的旧窗口只用于结算。
func OpenReplyWindow(ctx context.Context, tx bun.Tx, workspaceID, identityID string, window channeladapter.Window) (bool, error) {
	if _, err := tx.ExecContext(ctx, "SELECT id FROM channel_identities WHERE id = ? AND workspace_id = ? FOR UPDATE", identityID, workspaceID); err != nil {
		return false, err
	}
	newer, err := tx.NewSelect().Model((*models.ChannelReplyWindow)(nil)).
		Where("workspace_id = ? AND channel_identity_id = ? AND trigger = ? AND opened_at >= ?", workspaceID, identityID, window.Trigger, window.OpenedAt).
		Exists(ctx)
	if err != nil || newer {
		return false, err
	}
	// 创建时间取持有渠道身份锁之后的实际时刻，与投递认领时刻可比，平台拒绝发送时据此区分发送前已有的窗口。
	if _, err := tx.NewInsert().Model(&models.ChannelReplyWindow{
		WorkspaceID: workspaceID, ChannelIdentityID: identityID, Trigger: window.Trigger,
		OpenedAt: window.OpenedAt, ExpiresAt: window.ExpiresAt, Quota: window.Quota,
	}).ExcludeColumn("id", "updated_at").Value("created_at", "clock_timestamp()").Exec(ctx); err != nil {
		return false, err
	}
	_, err = tx.NewDelete().Model((*models.ChannelReplyWindow)(nil)).
		Where("workspace_id = ? AND channel_identity_id = ? AND trigger = ? AND opened_at < ? AND reserved = 0", workspaceID, identityID, window.Trigger, window.OpenedAt).
		Exec(ctx)
	return err == nil, err
}

// UsableReplyWindowCondition 返回别名回复窗口当前可用于再发送 count 个请求项的条件：同一渠道身份同一触发动作中开启时间最新、未到期且剩余额度足够。
func UsableReplyWindowCondition(alias string, count int) schema.QueryWithArgs {
	name := bun.Ident(alias)
	return bun.SafeQuery(`?.expires_at > now() AND (?.quota IS NULL OR ?.used + ?.reserved + ? <= ?.quota)
 AND NOT EXISTS (SELECT 1 FROM channel_reply_windows AS newer WHERE newer.workspace_id = ?.workspace_id AND newer.channel_identity_id = ?.channel_identity_id AND newer.trigger = ?.trigger AND newer.opened_at > ?.opened_at)`,
		name, name, name, name, count, name, name, name, name, name)
}

// reserveReplyWindow 在认领事务内为 count 个请求项预占渠道身份的回复窗口，只使用各触发动作最新开启、未到期且剩余额度足够的窗口，取最早到期者；没有可用窗口时返回 nil。
func reserveReplyWindow(ctx context.Context, tx bun.Tx, workspaceID, identityID string, count int) (*models.ChannelReplyWindow, error) {
	window := &models.ChannelReplyWindow{}
	err := tx.NewSelect().Model(window).
		Where("crw.workspace_id = ? AND crw.channel_identity_id = ?", workspaceID, identityID).
		Where("?", UsableReplyWindowCondition("crw", count)).
		OrderExpr("crw.expires_at, crw.id").Limit(1).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.NewUpdate().Model(window).Set("reserved = reserved + ?", count).WherePK().Exec(ctx); err != nil {
		return nil, err
	}
	return window, nil
}

// settleReplyWindow 结算投递当前请求项在回复窗口中的预占：平台已接受时计入已用并保留其余请求项的预占，结果未知时保留全部预占，其余结果释放全部未发送请求项的预占。
func settleReplyWindow(ctx context.Context, db bun.IDB, delivery *models.ChannelMessageDelivery, outcome channeladapter.Outcome) error {
	if delivery.ReplyWindowID == nil || outcome == channeladapter.OutcomeUncertain {
		return nil
	}
	var unsent int
	if err := db.NewSelect().Model((*models.ChannelDeliveryItem)(nil)).ColumnExpr("count(*)").
		Where("delivery_id = ? AND sent_at IS NULL", delivery.ID).Scan(ctx, &unsent); err != nil {
		return err
	}
	set, release := "used = used + 1, reserved = GREATEST(reserved - 1, 0)", unsent <= 1
	if outcome != channeladapter.OutcomeSent {
		set, release = "reserved = GREATEST(reserved - ?, 0)", true
	}
	query := db.NewUpdate().Model((*models.ChannelReplyWindow)(nil)).Where("id = ? AND workspace_id = ?", *delivery.ReplyWindowID, delivery.WorkspaceID)
	if outcome == channeladapter.OutcomeSent {
		query = query.Set(set)
	} else {
		query = query.Set(set, unsent)
	}
	if _, err := query.Exec(ctx); err != nil {
		return err
	}
	if release {
		delivery.ReplyWindowID = nil
	}
	return nil
}
