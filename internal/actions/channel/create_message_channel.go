//go:build server

package channel

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// CreateMessageChannelAction 创建消息渠道。
type CreateMessageChannelAction struct {
	db *bun.DB
}

// NewCreateMessageChannelAction 创建消息渠道操作。
func NewCreateMessageChannelAction(db *bun.DB) *CreateMessageChannelAction {
	return &CreateMessageChannelAction{db: db}
}

// Execute 创建消息渠道，并初始化当前渠道类型需要的设置；部署尚未配置微信第三方平台时不能创建授权接入公众号渠道，返回 ErrWechatPlatformRequired。
func (a *CreateMessageChannelAction) Execute(ctx context.Context, identity *servermodels.Identity, input CreateMessageChannelInput) (*MessageChannelRecord, error) {
	input, fields := normalizeCreateMessageChannelInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	channel := &servermodels.Channel{
		WorkspaceID:               identity.Workspace.ID,
		CreatedByUserID:           identity.User.ID,
		Type:                      string(input.Type),
		Name:                      input.Name,
		DefaultLocale:             string(input.DefaultLocale),
		InitialRoutingTargetType:  string(input.NewConversationTarget.Type),
		InitialRoutingTargetID:    routingTargetID(input.NewConversationTarget),
		FallbackRoutingTargetType: string(input.FallbackTarget.Type),
		FallbackRoutingTargetID:   routingTargetID(input.FallbackTarget),
		Description:               support.NilIfZero(input.Description),
	}
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := validateRoutingTarget(ctx, tx, identity.Workspace.ID, input.Type, "newConversationTarget", input.NewConversationTarget); err != nil {
			return err
		}
		if err := validateRoutingTarget(ctx, tx, identity.Workspace.ID, input.Type, "fallbackTarget", input.FallbackTarget); err != nil {
			return err
		}
		// 授权接入公众号渠道要求部署已配置微信第三方平台。
		if input.Type == domain.ChannelTypeWechatAuthorization {
			configured, err := tx.NewSelect().Model((*servermodels.WechatPlatform)(nil)).Exists(ctx)
			if err != nil {
				return fmt.Errorf("read wechat platform: %w", err)
			}
			if !configured {
				return ErrWechatPlatformRequired
			}
		}

		_, err := tx.NewInsert().
			Model(channel).
			Column("workspace_id", "created_by_user_id", "type", "name", "description", "default_locale", "initial_routing_target_type", "initial_routing_target_id", "fallback_routing_target_type", "fallback_routing_target_id").
			Returning("*").
			Exec(ctx)
		if err != nil {
			return err
		}
		switch input.Type {
		case domain.ChannelTypeWebsite:
			setting := &servermodels.WebsiteChannelSetting{
				ChannelID:   channel.ID,
				WorkspaceID: identity.Workspace.ID,
				ChatTitle:   channel.Name,
				ThemeColor:  DefaultWebsiteChannelThemeColor,
			}
			_, err = tx.NewInsert().
				Model(setting).
				Column("channel_id", "workspace_id", "chat_title", "theme_color").
				Exec(ctx)
			return err
		case domain.ChannelTypeTelegram:
			setting := &servermodels.TelegramChannelSetting{
				ChannelID:   channel.ID,
				WorkspaceID: identity.Workspace.ID,
			}
			_, err = tx.NewInsert().
				Model(setting).
				Column("channel_id", "workspace_id").
				Exec(ctx)
			return err
		default:
			return nil
		}
	})
	if err != nil {
		return nil, fmt.Errorf("create message channel: %w", err)
	}
	return NewMessageChannelRecord(channel), nil
}
