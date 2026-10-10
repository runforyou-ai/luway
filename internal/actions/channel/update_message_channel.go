//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// UpdateMessageChannelAction 分别修改消息渠道基础信息和接待设置。
type UpdateMessageChannelAction struct {
	db *bun.DB
}

// NewUpdateMessageChannelAction 创建消息渠道修改操作。
func NewUpdateMessageChannelAction(db *bun.DB) *UpdateMessageChannelAction {
	return &UpdateMessageChannelAction{db: db}
}

// ExecuteReception 校验并更新渠道接待设置。
func (a *UpdateMessageChannelAction) ExecuteReception(ctx context.Context, identity *servermodels.Identity, channelID string, input MessageChannelReceptionInput) (*MessageChannelRecord, error) {
	input, fields := normalizeMessageChannelReception(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}

	channel := &servermodels.Channel{}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 渠道路由变化后通知企业全部网站访客重新读取接待状态。
		realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Workspace.ID))
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 锁序为先路由目标身份、后渠道，与停用 AI 员工和转人工一致；渠道类型不可变，校验前按不加锁读取。
		query := tx.NewSelect().Model(channel).
			Column("id", "type").
			Where("c.id = ?", channelID).
			Where("c.workspace_id = ?", identity.Workspace.ID).
			Where("c.type IN (?)", bun.List(domain.MessageChannelTypes()))
		if err := query.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		channelType := domain.ChannelType(channel.Type)
		if err := validateRoutingTarget(ctx, tx, identity.Workspace.ID, channelType, "newConversationTarget", input.NewConversationTarget); err != nil {
			return err
		}
		if err := validateRoutingTarget(ctx, tx, identity.Workspace.ID, channelType, "fallbackTarget", input.FallbackTarget); err != nil {
			return err
		}
		if err := query.For("UPDATE").Scan(ctx); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		_, err := tx.NewUpdate().
			Model(channel).
			Set("initial_routing_target_type = ?", input.NewConversationTarget.Type).
			Set("initial_routing_target_id = ?", routingTargetID(input.NewConversationTarget)).
			Set("fallback_routing_target_type = ?", input.FallbackTarget.Type).
			Set("fallback_routing_target_id = ?", routingTargetID(input.FallbackTarget)).
			Where("c.id = ?", channelID).
			Where("c.workspace_id = ?", identity.Workspace.ID).
			Returning("*").
			Exec(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update message channel: %w", err)
	}
	return NewMessageChannelRecord(channel), nil
}

// ExecuteBasics 更新渠道名称、说明和默认语言。
func (a *UpdateMessageChannelAction) ExecuteBasics(ctx context.Context, identity *servermodels.Identity, channelID string, input MessageChannelBasicsInput) (*MessageChannelRecord, error) {
	input = normalizeMessageChannelBasics(input)
	channel := &servermodels.Channel{}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		err := tx.NewSelect().Model(channel).Column("id", "name").
			Where("c.id = ?", channelID).
			Where("c.workspace_id = ?", identity.Workspace.ID).
			Where("c.type IN (?)", bun.List(domain.MessageChannelTypes())).
			For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		previousName := channel.Name
		_, err = tx.NewUpdate().Model(channel).
			Set("name = ?", input.Name).
			Set("description = ?", support.NilIfZero(input.Description)).
			Set("default_locale = ?", input.DefaultLocale).
			Where("c.id = ?", channelID).
			Where("c.workspace_id = ?", identity.Workspace.ID).
			Returning("*").Exec(ctx)
		if err != nil {
			return err
		}
		if previousName != channel.Name {
			return chatstate.TouchChannelConversations(ctx, tx, identity.Workspace.ID, channelID)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update message channel basics: %w", err)
	}
	return NewMessageChannelRecord(channel), nil
}
