//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// CustomerToolDecider 在客户会话事务中由客户确认或拒绝 AI 员工提交的操作；subjectID 是客户的聊天主体。
type CustomerToolDecider interface {
	DecideForCustomer(ctx context.Context, tx bun.Tx, workspaceID, conversationID, subjectID, toolCallID string, approve bool) error
}

// WebsiteToolCallDecisionInput 定义网站访客对会话中 AI 员工操作的确认或拒绝。
type WebsiteToolCallDecisionInput struct {
	ChannelID      string
	ExternalID     string
	ConversationID string
	ToolCallID     string
	Approve        bool
}

// DecideWebsiteToolCallAction 由网站访客确认或拒绝 AI 员工在其会话中提交的操作。
type DecideWebsiteToolCallAction struct {
	db      *bun.DB
	decider CustomerToolDecider
}

// NewDecideWebsiteToolCallAction 创建网站访客操作确认 Action。
func NewDecideWebsiteToolCallAction(db *bun.DB, decider CustomerToolDecider) *DecideWebsiteToolCallAction {
	return &DecideWebsiteToolCallAction{db: db, decider: decider}
}

// Execute 在访客拥有的会话中以访客所属联系人的身份裁决操作；会话不属于访客时返回会话不存在。
func (a *DecideWebsiteToolCallAction) Execute(ctx context.Context, input WebsiteToolCallDecisionInput) error {
	fields := map[string]conversationaction.ValidationCode{}
	if !str.IsUUID(input.ChannelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if !customeridentity.ValidExternalID(input.ExternalID) {
		fields["visitorToken"] = ValidationExternalIDInvalid
	}
	if len(fields) > 0 {
		return &conversationaction.ValidationError{Fields: fields}
	}
	if !str.IsUUID(input.ConversationID) {
		return conversationaction.ErrConversationNotFound
	}
	channel, err := loadWebsiteChannel(ctx, a.db, input.ChannelID)
	if err != nil {
		return err
	}
	visitor, found, err := loadWebsiteVisitorIdentity(ctx, a.db, channel, input.ExternalID)
	if err != nil {
		return err
	}
	if !found || visitor.ContactID == nil {
		return conversationaction.ErrConversationNotFound
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		var subjectID string
		err := tx.NewSelect().TableExpr("channel_conversations AS cc").
			ColumnExpr("cs.id").
			Join("JOIN chat_subjects AS cs ON cs.workspace_id = cc.workspace_id AND cs.kind = ? AND cs.source_id = ?", domain.ChatSubjectKindContact, *visitor.ContactID).
			Where("cc.workspace_id = ? AND cc.conversation_id = ? AND cc.channel_identity_id = ?", channel.WorkspaceID, input.ConversationID, visitor.ID).
			Scan(ctx, &subjectID)
		if errors.Is(err, sql.ErrNoRows) {
			return conversationaction.ErrConversationNotFound
		}
		if err != nil {
			return fmt.Errorf("load website visitor chat subject: %w", err)
		}
		return a.decider.DecideForCustomer(ctx, tx, channel.WorkspaceID, input.ConversationID, subjectID, input.ToolCallID, input.Approve)
	})
}
