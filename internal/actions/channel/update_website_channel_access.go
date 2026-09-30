//go:build server

package channel

import (
	"context"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	input, fields := normalizeWebsiteChannelAccessInput(input)
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
			Set("allowed_embed_hosts = ?", pgdialect.Array(input.AllowedHosts)).
			Set("updated_at = now()").
			Where("wcs.channel_id = ?", channelID).
			Where("wcs.organization_id = ?", identity.Organization.ID).
			Returning("*").
			Scan(ctx)
	})
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update website channel access: %w", err)
	}
	record := websiteChannelSettingRecord(setting)
	return &record, nil
}
