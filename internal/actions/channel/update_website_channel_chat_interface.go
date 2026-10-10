//go:build server

package channel

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// UpdateWebsiteChannelChatInterfaceAction 修改网站渠道聊天界面。
type UpdateWebsiteChannelChatInterfaceAction struct {
	db *bun.DB
}

// NewUpdateWebsiteChannelChatInterfaceAction 创建聊天界面修改操作。
func NewUpdateWebsiteChannelChatInterfaceAction(db *bun.DB) *UpdateWebsiteChannelChatInterfaceAction {
	return &UpdateWebsiteChannelChatInterfaceAction{db: db}
}

// Execute 校验渠道归属并保存聊天界面设置。
func (a *UpdateWebsiteChannelChatInterfaceAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input WebsiteChannelChatInterfaceInput) (*WebsiteChannelSettingRecord, error) {
	input = normalizeWebsiteChannelChatInterfaceInput(input)

	setting := &servermodels.WebsiteChannelSetting{}
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockWebsiteChannel(ctx, tx, identity.Workspace.ID, channelID); err != nil {
			return err
		}
		return tx.NewUpdate().
			Model(setting).
			Set("chat_title = ?", input.Title).
			Set("greeting_message = ?", support.NilIfZero(input.GreetingMessage)).
			Set("theme_color = ?", input.ThemeColor).
			Set("attachments_enabled = ?", input.AttachmentsEnabled).
			Set("emoji_enabled = ?", input.EmojiEnabled).
			Set("rating_enabled = ?", input.RatingEnabled).
			Set("multiple_conversations_enabled = ?", input.MultipleConversationsEnabled).
			Where("wcs.channel_id = ?", channelID).
			Where("wcs.workspace_id = ?", identity.Workspace.ID).
			Returning("*").
			Scan(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("update website channel chat interface: %w", err)
	}
	record := websiteChannelSettingRecord(setting)
	return &record, nil
}
