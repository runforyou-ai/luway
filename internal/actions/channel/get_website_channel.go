//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetWebsiteChannelQuery 读取当前企业的单个网站渠道。
type GetWebsiteChannelQuery struct {
	db *bun.DB
}

// WebsiteChannelDetail 定义网站渠道详情和访客聊天界面设置。
type WebsiteChannelDetail struct {
	MessageChannelRecord
	ChatInterface WebsiteChannelSettingRecord `json:"chatInterface"`
	// HelpCenterKnowledgeBaseIDs 是帮助中心发布的知识库编号，按知识库名称排序。
	HelpCenterKnowledgeBaseIDs []string `json:"helpCenterKnowledgeBaseIds"`
}

// NewGetWebsiteChannelQuery 创建网站渠道详情查询。
func NewGetWebsiteChannelQuery(db *bun.DB) *GetWebsiteChannelQuery {
	return &GetWebsiteChannelQuery{db: db}
}

// Execute 返回当前企业的网站渠道详情。
func (q *GetWebsiteChannelQuery) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*WebsiteChannelDetail, error) {
	channel := &servermodels.Channel{}
	err := q.db.NewSelect().
		Model(channel).
		Where("c.id = ?", channelID).
		Where("c.workspace_id = ?", identity.Workspace.ID).
		Where("c.type = ?", domain.ChannelTypeWebsite).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get website channel: %w", err)
	}
	setting := servermodels.WebsiteChannelSetting{}
	if err := q.db.NewSelect().
		Model(&setting).
		Where("wcs.channel_id = ?", channelID).
		Where("wcs.workspace_id = ?", identity.Workspace.ID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("get website channel settings: %w", err)
	}
	knowledgeBaseIDs, err := websiteChannelKnowledgeBaseIDs(ctx, q.db, identity.Workspace.ID, channelID)
	if err != nil {
		return nil, fmt.Errorf("get website channel help center: %w", err)
	}
	return &WebsiteChannelDetail{
		MessageChannelRecord:       *NewMessageChannelRecord(channel),
		ChatInterface:              websiteChannelSettingRecord(&setting),
		HelpCenterKnowledgeBaseIDs: knowledgeBaseIDs,
	}, nil
}

// lockWebsiteChannel 锁定当前企业的网站渠道行，渠道不存在时返回 ErrNotFound。
func lockWebsiteChannel(ctx context.Context, tx bun.Tx, workspaceID, channelID string) error {
	err := tx.NewSelect().
		Model((*servermodels.Channel)(nil)).
		Column("id").
		Where("c.id = ?", channelID).
		Where("c.workspace_id = ?", workspaceID).
		Where("c.type = ?", domain.ChannelTypeWebsite).
		For("UPDATE").
		Scan(ctx, new(string))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
