//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetPublicWebsiteChannelQuery 按公开标识读取网站渠道。
type GetPublicWebsiteChannelQuery struct {
	db *bun.DB
}

// PublicWebsiteChannel 定义访客入口所需的网站渠道信息。
type PublicWebsiteChannel struct {
	ID                string
	Title             string
	Greeting          string
	ThemeColor        string
	AllowedEmbedHosts []string
	DefaultLocale     domain.CustomerLocale
	// HomeEnabled 与 HelpEnabled 控制首页与帮助页签，帮助页签还需要发布知识库。
	HomeEnabled bool
	HelpEnabled bool
	// HomeWelcome 与 HomeHeadline 是首页问候语，为空时使用默认文案。
	HomeWelcome        string
	HomeHeadline       string
	HomeBlocks         []domain.WebsiteHomeBlock
	HomeLinks          []domain.WebsiteHomeLink
	AttachmentsEnabled bool
	EmojiEnabled       bool
	RatingEnabled      bool
	// MultipleConversationsEnabled 为假时访客界面只提供一个对话。
	MultipleConversationsEnabled bool
}

// NewGetPublicWebsiteChannelQuery 创建公开网站渠道查询。
func NewGetPublicWebsiteChannelQuery(db *bun.DB) *GetPublicWebsiteChannelQuery {
	return &GetPublicWebsiteChannelQuery{db: db}
}

// Execute 返回已启用的网站渠道访客界面设置。
func (q *GetPublicWebsiteChannelQuery) Execute(ctx context.Context, channelID string) (*PublicWebsiteChannel, error) {
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	channel := &servermodels.Channel{}
	err := q.db.NewSelect().
		Model(channel).
		Column("id", "default_locale").
		Where("c.id = ?", channelID).
		Where("c.type = ?", domain.ChannelTypeWebsite).
		Where("c.enabled = TRUE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get public website channel: %w", err)
	}
	setting := servermodels.WebsiteChannelSetting{}
	if err := q.db.NewSelect().
		Model(&setting).
		Where("wcs.channel_id = ?", channel.ID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("get public website channel settings: %w", err)
	}
	return &PublicWebsiteChannel{
		ID:                           channel.ID,
		Title:                        setting.ChatTitle,
		Greeting:                     common.StringValue(setting.GreetingMessage),
		ThemeColor:                   setting.ThemeColor,
		AllowedEmbedHosts:            setting.AllowedEmbedHosts,
		DefaultLocale:                domain.CustomerLocale(channel.DefaultLocale),
		HomeEnabled:                  setting.HomeEnabled,
		HelpEnabled:                  setting.HelpEnabled,
		HomeWelcome:                  common.StringValue(setting.HomeWelcome),
		HomeHeadline:                 common.StringValue(setting.HomeHeadline),
		HomeBlocks:                   setting.HomeBlocks,
		HomeLinks:                    setting.HomeLinks,
		AttachmentsEnabled:           setting.AttachmentsEnabled,
		EmojiEnabled:                 setting.EmojiEnabled,
		RatingEnabled:                setting.RatingEnabled,
		MultipleConversationsEnabled: setting.MultipleConversationsEnabled,
	}, nil
}
