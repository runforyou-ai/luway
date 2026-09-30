//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
)

// WebsiteChannelSetting 表示网站渠道的访客聊天界面设置。
type WebsiteChannelSetting struct {
	bun.BaseModel `bun:"table:website_channel_settings,alias:wcs"`

	ChannelID         string   `bun:"channel_id,pk"`
	OrganizationID    string   `bun:"organization_id"`
	ChatTitle         string   `bun:"chat_title"`
	GreetingMessage   *string  `bun:"greeting_message"`
	ThemeColor        string   `bun:"theme_color"`
	AllowedEmbedHosts []string `bun:"allowed_embed_hosts,array"`
	// HomeEnabled 与 HelpEnabled 控制首页与帮助页签，帮助页签还需要发布知识库。
	HomeEnabled bool `bun:"home_enabled"`
	HelpEnabled bool `bun:"help_enabled"`
	// HomeWelcome 与 HomeHeadline 是首页问候语的两行，为空时使用默认文案。
	HomeWelcome  *string `bun:"home_welcome"`
	HomeHeadline *string `bun:"home_headline"`
	// HomeBlocks 是首页卡片的展示顺序与开关，HomeLinks 是链接卡片中的链接。
	HomeBlocks         []domain.WebsiteHomeBlock `bun:"home_blocks,type:jsonb"`
	HomeLinks          []domain.WebsiteHomeLink  `bun:"home_links,type:jsonb"`
	AttachmentsEnabled bool                      `bun:"attachments_enabled"`
	EmojiEnabled       bool                      `bun:"emoji_enabled"`
	RatingEnabled      bool                      `bun:"rating_enabled"`
	// MultipleConversationsEnabled 为假时访客界面只提供一个对话。
	MultipleConversationsEnabled bool      `bun:"multiple_conversations_enabled"`
	CreatedAt                    time.Time `bun:"created_at"`
	UpdatedAt                    time.Time `bun:"updated_at"`
}
