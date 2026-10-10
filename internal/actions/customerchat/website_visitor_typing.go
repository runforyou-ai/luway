//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ReportWebsiteVisitorTypingAction 校验网站访客对客户线程的访问资格，并向企业客服发布访客输入状态。
type ReportWebsiteVisitorTypingAction struct {
	db *bun.DB
}

// NewReportWebsiteVisitorTypingAction 创建访客输入状态上报 Action。
func NewReportWebsiteVisitorTypingAction(db *bun.DB) *ReportWebsiteVisitorTypingAction {
	return &ReportWebsiteVisitorTypingAction{db: db}
}

// Execute 要求访客身份拥有该客户线程，向企业客服共享受众发布输入状态；无资格时返回 ErrConversationNotFound。
func (a *ReportWebsiteVisitorTypingAction) Execute(ctx context.Context, channelID, externalID, conversationID string, active bool) error {
	if !str.IsUUID(conversationID) {
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
	var senderSubjectID string
	err = a.db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("cs.id").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cc.workspace_id AND cs.kind = ? AND cs.source_id = ?", domain.ChatSubjectKindContact, visitor.ContactID).
		Where("cc.workspace_id = ? AND cc.conversation_id = ?", channel.WorkspaceID, conversationID).
		Where("cc.channel_identity_id = ?", visitor.ID).
		Scan(ctx, &senderSubjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return conversationaction.ErrConversationNotFound
	}
	if err != nil {
		return fmt.Errorf("authorize website visitor typing: %w", err)
	}
	realtime.Publish(realtime.ServiceInboxConversationTyping(channel.WorkspaceID, conversationID, senderSubjectID, active))
	return nil
}
