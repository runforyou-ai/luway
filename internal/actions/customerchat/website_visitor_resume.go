//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// MarkWebsiteConversationReadAction 记录网站访客在客户线程中已读到的位置。
type MarkWebsiteConversationReadAction struct {
	db *bun.DB
}

// NewMarkWebsiteConversationReadAction 创建访客已读位置上报 Action。
func NewMarkWebsiteConversationReadAction(db *bun.DB) *MarkWebsiteConversationReadAction {
	return &MarkWebsiteConversationReadAction{db: db}
}

// Execute 要求访客身份拥有该客户线程，只向前推进客户已读位置且不超过会话最新消息序号；无资格时返回 ErrConversationNotFound。
func (a *MarkWebsiteConversationReadAction) Execute(ctx context.Context, channelID, externalID, conversationID string, messageSeq int64) error {
	if !common.ValidUUID(channelID) {
		return conversationaction.ErrChannelNotFound
	}
	if !common.ValidUUID(conversationID) || messageSeq <= 0 {
		return conversationaction.ErrConversationNotFound
	}
	channel, err := loadWebsiteChannel(ctx, a.db, channelID)
	if err != nil {
		return err
	}
	visitor, found, err := loadWebsiteVisitorIdentity(ctx, a.db, channel, externalID)
	if err != nil {
		return err
	}
	if !found {
		return conversationaction.ErrConversationNotFound
	}
	owned, err := a.db.NewSelect().Model((*servermodels.ChannelConversation)(nil)).
		Where("cc.organization_id = ? AND cc.conversation_id = ? AND cc.contact_channel_identity_id = ?", channel.OrganizationID, conversationID, visitor.ID).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("authorize website visitor read: %w", err)
	}
	if !owned {
		return conversationaction.ErrConversationNotFound
	}
	latest := a.db.NewSelect().Model((*servermodels.Conversation)(nil)).Column("cv.last_message_seq").
		Where("cv.organization_id = ? AND cv.id = ?", channel.OrganizationID, conversationID)
	if _, err := a.db.NewUpdate().Model((*servermodels.ChannelConversation)(nil)).
		Set("contact_read_seq = LEAST(?, (?))", messageSeq, latest).
		Set("updated_at = now()").
		Where("organization_id = ? AND conversation_id = ? AND contact_read_seq < LEAST(?, (?))", channel.OrganizationID, conversationID, messageSeq, latest).
		Exec(ctx); err != nil {
		return fmt.Errorf("advance website visitor read position: %w", err)
	}
	return nil
}

// ResumedWebsiteVisitor 是回访令牌换得的匿名访客令牌与要打开的客户会话摘要。
type ResumedWebsiteVisitor struct {
	VisitorToken string
	Conversation ConversationSummary
}

// ResumeWebsiteVisitorQuery 用邮件中的回访令牌恢复匿名访客身份。
type ResumeWebsiteVisitorQuery struct {
	db *bun.DB
}

// NewResumeWebsiteVisitorQuery 创建回访令牌换取 Query。
func NewResumeWebsiteVisitorQuery(db *bun.DB) *ResumeWebsiteVisitorQuery {
	return &ResumeWebsiteVisitorQuery{db: db}
}

// Execute 校验令牌未过期且属于该网站渠道的匿名访客，返回该访客的令牌与会话摘要；令牌可在有效期内重复使用，无效时返回 ErrConversationNotFound。
func (q *ResumeWebsiteVisitorQuery) Execute(ctx context.Context, channelID, token string) (ResumedWebsiteVisitor, error) {
	if !common.ValidUUID(channelID) {
		return ResumedWebsiteVisitor{}, conversationaction.ErrChannelNotFound
	}
	channel, err := loadWebsiteChannel(ctx, q.db, channelID)
	if err != nil {
		return ResumedWebsiteVisitor{}, err
	}
	var row struct {
		ConversationID    string `bun:"conversation_id"`
		ChannelIdentityID string `bun:"channel_identity_id"`
		ExternalID        string `bun:"external_id"`
	}
	err = q.db.NewSelect().TableExpr("conversation_resume_tokens AS crt").
		ColumnExpr("crt.conversation_id, cci.id AS channel_identity_id, cci.external_id").
		Join("JOIN contact_channel_identities AS cci ON cci.organization_id = crt.organization_id AND cci.id = crt.contact_channel_identity_id").
		Where("crt.token_hash = ? AND crt.expires_at > now()", customernotify.ResumeTokenHash(strings.TrimSpace(token))).
		Where("crt.organization_id = ? AND cci.channel_id = ?", channel.OrganizationID, channel.ID).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return ResumedWebsiteVisitor{}, conversationaction.ErrConversationNotFound
	}
	if err != nil {
		return ResumedWebsiteVisitor{}, fmt.Errorf("load conversation resume token: %w", err)
	}
	kind, visitorToken, _ := customeridentity.ParseExternalID(row.ExternalID)
	if kind != customeridentity.ExternalIDAnonymous {
		return ResumedWebsiteVisitor{}, conversationaction.ErrConversationNotFound
	}
	summary, err := loadConversationSummary(ctx, q.db, channel.OrganizationID, row.ConversationID, row.ChannelIdentityID)
	if err != nil {
		return ResumedWebsiteVisitor{}, err
	}
	if err := resolveSummaryReception(ctx, chatstate.NewReceptionResolver(q.db, channel.OrganizationID, time.Now()), &summary); err != nil {
		return ResumedWebsiteVisitor{}, err
	}
	return ResumedWebsiteVisitor{VisitorToken: visitorToken, Conversation: summary}, nil
}
