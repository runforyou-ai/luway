//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	input, fields := normalizeMessageChannelReception(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}

	channel := &servermodels.Channel{}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 渠道路由变化后通知企业全部网站访客重新读取接待状态。
		realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Organization.ID))
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 锁序为先路由目标身份、后渠道，与停用 AI 员工和转人工一致；渠道类型不可变，校验前按不加锁读取。
		query := tx.NewSelect().Model(channel).
			Column("id", "type").
			Where("c.id = ?", channelID).
			Where("c.organization_id = ?", identity.Organization.ID).
			Where("c.type IN (?)", bun.In(domain.MessageChannelTypes()))
		if err := query.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		channelType := domain.ChannelType(channel.Type)
		if err := validateRoutingTarget(ctx, tx, identity.Organization.ID, channelType, "newConversationTarget", input.NewConversationTarget); err != nil {
			return err
		}
		if err := validateRoutingTarget(ctx, tx, identity.Organization.ID, channelType, "fallbackTarget", input.FallbackTarget); err != nil {
			return err
		}
		if err := query.For("UPDATE").Scan(ctx); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		result, err := tx.NewUpdate().
			Model(channel).
			Set("initial_routing_target_type = ?", input.NewConversationTarget.Type).
			Set("initial_routing_target_id = ?", routingTargetID(input.NewConversationTarget)).
			Set("fallback_routing_target_type = ?", input.FallbackTarget.Type).
			Set("fallback_routing_target_id = ?", routingTargetID(input.FallbackTarget)).
			Set("updated_at = now()").
			Where("c.id = ?", channelID).
			Where("c.organization_id = ?", identity.Organization.ID).
			Where("c.type IN (?)", bun.In(domain.MessageChannelTypes())).
			Returning("*").
			Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update message channel: %w", err)
	}
	return messageChannelRecord(channel), nil
}

// ExecuteBasics 更新渠道名称、说明和默认语言。
func (a *UpdateMessageChannelAction) ExecuteBasics(ctx context.Context, identity *servermodels.Identity, channelID string, input MessageChannelBasicsInput) (*MessageChannelRecord, error) {
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	input, fields := normalizeMessageChannelBasics(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	channel := &servermodels.Channel{}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		err := tx.NewSelect().Model(channel).Column("id", "name").
			Where("c.id = ?", channelID).
			Where("c.organization_id = ?", identity.Organization.ID).
			Where("c.type IN (?)", bun.In(domain.MessageChannelTypes())).
			For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		previousName := channel.Name
		var description *string
		if input.Description != "" {
			description = &input.Description
		}
		_, err = tx.NewUpdate().Model(channel).
			Set("name = ?", input.Name).
			Set("description = ?", description).
			Set("default_locale = ?", input.DefaultLocale).
			Set("updated_at = now()").
			Where("c.id = ?", channelID).
			Where("c.organization_id = ?", identity.Organization.ID).
			Returning("*").Exec(ctx)
		if err != nil {
			return err
		}
		if previousName != channel.Name {
			return chatstate.TouchChannelConversations(ctx, tx, identity.Organization.ID, channelID)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update message channel basics: %w", err)
	}
	return messageChannelRecord(channel), nil
}
