//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// WebsiteChannelKnowledgeBase 表示网站渠道帮助中心发布的一个知识库。
type WebsiteChannelKnowledgeBase struct {
	bun.BaseModel `bun:"table:website_channel_knowledge_bases,alias:wckb"`

	ChannelID       string    `bun:"channel_id,pk"`
	KnowledgeBaseID string    `bun:"knowledge_base_id,pk"`
	OrganizationID  string    `bun:"organization_id"`
	CreatedAt       time.Time `bun:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at"`
}
