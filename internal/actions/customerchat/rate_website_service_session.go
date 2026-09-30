//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	"github.com/runforyou-ai/cervi/internal/actions/knowledgegap"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/common/customeridentity"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

const maxRatingCommentLength = 1000

// WebsiteServiceSessionRatingInput 定义网站访客对已关闭服务周期的评价。
type WebsiteServiceSessionRatingInput struct {
	ChannelID        string
	ExternalID       string
	ConversationID   string
	ServiceSessionID string
	Resolved         bool
	Comment          string
}

// RateWebsiteServiceSessionAction 保存网站访客对已关闭服务周期的评价。
type RateWebsiteServiceSessionAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewRateWebsiteServiceSessionAction 创建网站访客评价 Action。
func NewRateWebsiteServiceSessionAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *RateWebsiteServiceSessionAction {
	return &RateWebsiteServiceSessionAction{db: db, enqueuer: enqueuer}
}

// Execute 在会话锁内写入周期评价并追加仅成员可见的评价事件，AI 员工关闭的周期评价为未解决时登记待补知识；每个周期只能评价一次，周期须处于关闭状态。
func (a *RateWebsiteServiceSessionAction) Execute(ctx context.Context, input WebsiteServiceSessionRatingInput) (VisitorRating, error) {
	input.Comment = strings.TrimSpace(input.Comment)
	fields := map[string]conversationaction.ValidationCode{}
	if !common.ValidUUID(input.ChannelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if !customeridentity.ValidExternalID(input.ExternalID) {
		fields["visitorToken"] = ValidationExternalIDInvalid
	}
	if utf8.RuneCountInString(input.Comment) > maxRatingCommentLength {
		fields["comment"] = ValidationRatingCommentTooLong
	}
	if len(fields) > 0 {
		return VisitorRating{}, &conversationaction.ValidationError{Fields: fields}
	}
	if !common.ValidUUID(input.ConversationID) || !common.ValidUUID(input.ServiceSessionID) {
		return VisitorRating{}, conversationaction.ErrConversationNotFound
	}
	channel, err := loadWebsiteChannel(ctx, a.db, input.ChannelID)
	if err != nil {
		return VisitorRating{}, err
	}
	visitor, found, err := loadWebsiteVisitorIdentity(ctx, a.db, channel, input.ExternalID)
	if err != nil {
		return VisitorRating{}, err
	}
	if !found {
		return VisitorRating{}, conversationaction.ErrConversationNotFound
	}
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockChannelConversation(ctx, tx, channel.OrganizationID, input.ConversationID)
		if err != nil {
			return err
		}
		session := &servermodels.ServiceSession{}
		err = tx.NewSelect().Model(session).
			Where("ss.organization_id = ? AND ss.conversation_id = ? AND ss.id = ?", channel.OrganizationID, conversation.ID, input.ServiceSessionID).
			Where("EXISTS (SELECT 1 FROM channel_conversations AS cc WHERE cc.organization_id = ss.organization_id AND cc.conversation_id = ss.conversation_id AND cc.contact_channel_identity_id = ?)", visitor.ID).
			For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return conversationaction.ErrConversationNotFound
		}
		if err != nil {
			return fmt.Errorf("lock rated service session: %w", err)
		}
		if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusClosed || session.RatedAt != nil {
			return &conversationaction.ConflictError{Reason: ConflictReasonServiceSessionNotRateable}
		}
		ratedAt := time.Now().UTC()
		if _, err := tx.NewUpdate().Model(session).
			Set("rating_resolved = ?", input.Resolved).
			Set("rating_comment = ?", input.Comment).
			Set("rated_at = ?", ratedAt).
			Set("updated_at = now()").
			WherePK().
			Where("organization_id = ?", channel.OrganizationID).
			Exec(ctx); err != nil {
			return fmt.Errorf("save service session rating: %w", err)
		}
		payload, err := json.Marshal(domain.ServiceSessionRatedEvent{ServiceSessionID: session.ID, Resolved: input.Resolved, Comment: input.Comment})
		if err != nil {
			return fmt.Errorf("encode service session rated event: %w", err)
		}
		typeName := string(domain.ConversationSystemEventServiceSessionRated)
		eventID := uuid.NewV7().String()
		if _, _, err := chatstate.AppendMessage(ctx, tx, conversation, &servermodels.Message{
			ID: eventID, OrganizationID: session.OrganizationID, ConversationID: session.ConversationID,
			ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
			SystemEventType: &typeName, SystemEventPayload: payload, OriginatedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("append service session rated event: %w", err)
		}
		realtime.Notify(ctx, realtime.ServiceInboxReportsChanged(session.OrganizationID))
		if !input.Resolved {
			return knowledgegap.RecordRatedUnresolved(ctx, tx, a.enqueuer, session, eventID, ratedAt)
		}
		return nil
	})
	if err != nil {
		return VisitorRating{}, err
	}
	return VisitorRating{Resolved: &input.Resolved, Comment: input.Comment}, nil
}
