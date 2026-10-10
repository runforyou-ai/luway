//go:build server

package channel

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// UpdateWebsiteChannelAccessAction 修改网站渠道允许使用的网站。
type UpdateWebsiteChannelAccessAction struct {
	db *bun.DB
}

// NewUpdateWebsiteChannelAccessAction 创建允许网站修改操作。
func NewUpdateWebsiteChannelAccessAction(db *bun.DB) *UpdateWebsiteChannelAccessAction {
	return &UpdateWebsiteChannelAccessAction{db: db}
}

// Execute 校验渠道归属并保存允许嵌入的主机。
func (a *UpdateWebsiteChannelAccessAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input WebsiteChannelAccessInput) (*WebsiteChannelSettingRecord, error) {
	input, fields := normalizeWebsiteChannelAccessInput(input)
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
			Set("allowed_embed_hosts = ?", pgdialect.Array(input.AllowedHosts)).
			Where("wcs.channel_id = ?", channelID).
			Where("wcs.workspace_id = ?", identity.Workspace.ID).
			Returning("*").
			Scan(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("update website channel access: %w", err)
	}
	record := websiteChannelSettingRecord(setting)
	return &record, nil
}
