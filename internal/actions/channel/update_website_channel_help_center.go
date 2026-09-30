//go:build server

package channel

import (
	"context"
	"fmt"
	"slices"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// WebsiteChannelHelpCenterRecord 定义网站渠道帮助页签开关与发布的知识库，知识库编号按名称排序。
type WebsiteChannelHelpCenterRecord struct {
	Enabled          bool
	KnowledgeBaseIDs []string
}

// UpdateWebsiteChannelHelpCenterAction 修改网站渠道帮助页签开关与发布的知识库。
type UpdateWebsiteChannelHelpCenterAction struct {
	db *bun.DB
}

// NewUpdateWebsiteChannelHelpCenterAction 创建帮助中心发布范围修改操作。
func NewUpdateWebsiteChannelHelpCenterAction(db *bun.DB) *UpdateWebsiteChannelHelpCenterAction {
	return &UpdateWebsiteChannelHelpCenterAction{db: db}
}

// Execute 校验渠道与知识库归属后保存帮助页签开关，并整体替换发布的知识库。
func (a *UpdateWebsiteChannelHelpCenterAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input WebsiteChannelHelpCenterInput) (*WebsiteChannelHelpCenterRecord, error) {
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	// 规范化知识库编号并去重。
	ids := make([]string, 0, len(input.KnowledgeBaseIDs))
	for _, value := range input.KnowledgeBaseIDs {
		id, valid := common.NormalizeUUID(value)
		if !valid {
			return nil, &ValidationError{Fields: map[string]ValidationCode{"knowledgeBaseIds": ValidationKnowledgeBaseInvalid}}
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)

	var published []string
	var enabled bool
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockWebsiteChannel(ctx, tx, identity.Organization.ID, channelID); err != nil {
			return err
		}
		// 共享锁定知识库，与知识库删除互斥。
		if len(ids) > 0 {
			var found []string
			if err := tx.NewSelect().
				Model((*servermodels.KnowledgeBase)(nil)).
				Column("kb.id").
				Where("kb.organization_id = ? AND kb.id IN (?)", identity.Organization.ID, bun.In(ids)).
				For("SHARE").
				Scan(ctx, &found); err != nil {
				return err
			}
			if len(found) != len(ids) {
				return &ValidationError{Fields: map[string]ValidationCode{"knowledgeBaseIds": ValidationKnowledgeBaseInvalid}}
			}
		}
		if _, err := tx.NewDelete().
			Model((*servermodels.WebsiteChannelKnowledgeBase)(nil)).
			Where("channel_id = ? AND organization_id = ?", channelID, identity.Organization.ID).
			Exec(ctx); err != nil {
			return err
		}
		if len(ids) > 0 {
			rows := make([]servermodels.WebsiteChannelKnowledgeBase, 0, len(ids))
			for _, id := range ids {
				rows = append(rows, servermodels.WebsiteChannelKnowledgeBase{ChannelID: channelID, KnowledgeBaseID: id, OrganizationID: identity.Organization.ID})
			}
			if _, err := tx.NewInsert().Model(&rows).Column("channel_id", "knowledge_base_id", "organization_id").Exec(ctx); err != nil {
				return err
			}
		}
		if err := tx.NewUpdate().
			Model((*servermodels.WebsiteChannelSetting)(nil)).
			Set("help_enabled = ?", input.Enabled).
			Set("updated_at = now()").
			Where("wcs.channel_id = ? AND wcs.organization_id = ?", channelID, identity.Organization.ID).
			Returning("help_enabled").
			Scan(ctx, &enabled); err != nil {
			return err
		}
		var err error
		published, err = websiteChannelKnowledgeBaseIDs(ctx, tx, identity.Organization.ID, channelID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update website channel help center: %w", err)
	}
	return &WebsiteChannelHelpCenterRecord{Enabled: enabled, KnowledgeBaseIDs: published}, nil
}

// websiteChannelKnowledgeBaseIDs 返回网站渠道帮助中心发布的知识库编号，按知识库名称排序。
func websiteChannelKnowledgeBaseIDs(ctx context.Context, db bun.IDB, organizationID, channelID string) ([]string, error) {
	ids := make([]string, 0)
	err := db.NewSelect().
		Model((*servermodels.WebsiteChannelKnowledgeBase)(nil)).
		ColumnExpr("wckb.knowledge_base_id").
		Join("JOIN knowledge_bases AS kb ON kb.id = wckb.knowledge_base_id AND kb.organization_id = wckb.organization_id").
		Where("wckb.channel_id = ? AND wckb.organization_id = ?", channelID, organizationID).
		OrderExpr("kb.name, kb.id").
		Scan(ctx, &ids)
	return ids, err
}
