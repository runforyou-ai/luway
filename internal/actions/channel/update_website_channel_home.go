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

// UpdateWebsiteChannelHomeAction 修改网站渠道 Messenger 首页。
type UpdateWebsiteChannelHomeAction struct {
	db *bun.DB
}

// NewUpdateWebsiteChannelHomeAction 创建 Messenger 首页修改操作。
func NewUpdateWebsiteChannelHomeAction(db *bun.DB) *UpdateWebsiteChannelHomeAction {
	return &UpdateWebsiteChannelHomeAction{db: db}
}

// Execute 校验渠道归属并保存首页开关、问候语、卡片顺序与链接，问候语为空时使用默认文案。
func (a *UpdateWebsiteChannelHomeAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input WebsiteChannelHomeInput) (*WebsiteChannelSettingRecord, error) {
	input, fields := normalizeWebsiteChannelHomeInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
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
			Set("home_enabled = ?", input.Enabled).
			Set("home_welcome = ?", support.NilIfZero(input.Welcome)).
			Set("home_headline = ?", support.NilIfZero(input.Headline)).
			Set("home_blocks = ?", input.Blocks).
			Set("home_links = ?", input.Links).
			Where("wcs.channel_id = ?", channelID).
			Where("wcs.workspace_id = ?", identity.Workspace.ID).
			Returning("*").
			Scan(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("update website channel home: %w", err)
	}
	record := websiteChannelSettingRecord(setting)
	return &record, nil
}
