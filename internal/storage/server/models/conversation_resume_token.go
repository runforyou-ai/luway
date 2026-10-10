//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ConversationResumeToken 表示邮件「继续对话」链接使用的客户会话回访令牌。
type ConversationResumeToken struct {
	bun.BaseModel `bun:"table:conversation_resume_tokens,alias:crt"`

	ID                string    `bun:"id,pk"`
	CreatedAt         time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt         time.Time `bun:"updated_at,nullzero,default:now()"`
	WorkspaceID       string    `bun:"workspace_id"`
	ConversationID    string    `bun:"conversation_id"`
	ChannelIdentityID string    `bun:"channel_identity_id"`
	TokenHash         string    `bun:"token_hash"`
	ExpiresAt         time.Time `bun:"expires_at"`
}
