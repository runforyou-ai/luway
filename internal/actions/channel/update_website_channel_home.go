//go:build server

package channel

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	input, fields := normalizeWebsiteChannelHomeInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	setting := &servermodels.WebsiteChannelSetting{}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockWebsiteChannel(ctx, tx, identity.Organization.ID, channelID); err != nil {
			return err
		}
		return tx.NewUpdate().
			Model(setting).
			Set("home_enabled = ?", input.Enabled).
			Set("home_welcome = ?", common.OptionalString(input.Welcome)).
			Set("home_headline = ?", common.OptionalString(input.Headline)).
			Set("home_blocks = ?", input.Blocks).
			Set("home_links = ?", input.Links).
			Set("updated_at = now()").
			Where("wcs.channel_id = ?", channelID).
			Where("wcs.organization_id = ?", identity.Organization.ID).
			Returning("*").
			Scan(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("update website channel home: %w", err)
	}
	record := websiteChannelSettingRecord(setting)
	return &record, nil
}
