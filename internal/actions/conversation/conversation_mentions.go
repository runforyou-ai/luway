//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ConversationNavigationState 表示会话可见尾端及个人提及进度。
type ConversationNavigationState struct {
	PendingMentionCount      int
	ReviewedThroughMessageID *string
	ReviewedThroughSequence  int64
	LatestMessageID          *string
	LatestSequence           int64
}

// PendingConversationMentions 表示一轮固定的轻量提及队列。
type PendingConversationMentions struct {
	MessageIDs         []string
	LastTargetSequence *int64
}

// ConversationMentionReview 表示单条查看确认及连续水位。
type ConversationMentionReview struct {
	ReviewedThroughMessageID *string
	ReviewedThroughSequence  int64
	Outcome                  string
}

// GetConversationNavigationStateQuery 读取群聊或客户会话的导航状态。
type GetConversationNavigationStateQuery struct{ db *bun.DB }

// ListPendingConversationMentionsQuery 读取尚未查看的提及。
type ListPendingConversationMentionsQuery struct{ db *bun.DB }

// MarkConversationMentionReviewedAction 确认个人已查看的会话提及。
type MarkConversationMentionReviewedAction struct{ db *bun.DB }

// NewGetConversationNavigationStateQuery 创建会话导航状态查询。
func NewGetConversationNavigationStateQuery(db *bun.DB) *GetConversationNavigationStateQuery {
	return &GetConversationNavigationStateQuery{db: db}
}

// NewListPendingConversationMentionsQuery 创建提及队列查询。
func NewListPendingConversationMentionsQuery(db *bun.DB) *ListPendingConversationMentionsQuery {
	return &ListPendingConversationMentionsQuery{db: db}
}

// NewMarkConversationMentionReviewedAction 创建提及查看确认命令。
func NewMarkConversationMentionReviewedAction(db *bun.DB) *MarkConversationMentionReviewedAction {
	return &MarkConversationMentionReviewedAction{db: db}
}

// mentionNavigationQuery 限定当前用户可阅读的群聊或服务会话，并关联本人聊天主体和提及水位。
func mentionNavigationQuery(db bun.IDB, identity *servermodels.Identity, conversationID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("conversations AS cv").
		Join("LEFT JOIN chat_subjects AS mine ON mine.organization_id = cv.organization_id AND mine.kind = ? AND mine.source_id = ?", domain.ChatSubjectKindOrganizationIdentity, identity.OrganizationIdentity.ID).
		Join("LEFT JOIN conversation_user_states AS state ON state.organization_id = cv.organization_id AND state.conversation_id = cv.id AND state.user_id = ?", identity.User.ID).
		Join("LEFT JOIN messages AS reviewed ON reviewed.organization_id = cv.organization_id AND reviewed.conversation_id = cv.id AND reviewed.id = state.last_reviewed_mention_message_id").
		Where("cv.organization_id = ? AND cv.id = ?", identity.Organization.ID, conversationID).
		// 群聊要求本人仍是成员，客户会话与其他服务会话按企业边界阅读。
		Where(`cv.type = ? OR EXISTS (
			SELECT 1 FROM service_conversations AS svc WHERE svc.organization_id = cv.organization_id AND svc.conversation_id = cv.id
		) OR (cv.type = ? AND cv.status IN (?, ?) AND EXISTS (
			SELECT 1 FROM conversation_participants AS member
			WHERE member.organization_id = cv.organization_id AND member.conversation_id = cv.id AND member.subject_id = mine.id AND member.left_at IS NULL
		))`, domain.ConversationTypeChannel, domain.ConversationTypeGroup, domain.ConversationStatusActive, domain.ConversationStatusArchived)
}

// pendingMentionsQuery 共用提及资格，排除本人消息、连续已查看范围和单条查看记录。
func pendingMentionsQuery(db bun.IDB, userID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("messages AS pending").
		Join("JOIN conversation_participants AS sender ON sender.organization_id = pending.organization_id AND sender.conversation_id = pending.conversation_id AND sender.id = pending.sender_participant_id").
		Where("pending.organization_id = cv.organization_id AND pending.conversation_id = cv.id").
		Where("pending.deleted_at IS NULL AND sender.subject_id <> mine.id").
		Where("pending.message_seq > COALESCE(reviewed.message_seq, 0)").
		Where("NOT EXISTS (SELECT 1 FROM conversation_mention_reviews AS receipt WHERE receipt.organization_id = pending.organization_id AND receipt.conversation_id = pending.conversation_id AND receipt.user_id = ? AND receipt.message_id = pending.id)", userID).
		Where(`pending.mention_all OR EXISTS (SELECT 1 FROM message_mentions AS mention WHERE mention.organization_id = pending.organization_id AND mention.message_id = pending.id AND mention.subject_id = mine.id)`)
}

// Execute 在单个查询快照内统计待查看提及并读取最新可见消息。
func (q *GetConversationNavigationStateQuery) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (ConversationNavigationState, error) {
	if !common.ValidUUID(conversationID) {
		return ConversationNavigationState{}, &ValidationError{Fields: map[string]ValidationCode{"conversationId": ValidationConversationIDInvalid}}
	}
	var result ConversationNavigationState
	err := mentionNavigationQuery(q.db, identity, conversationID).
		ColumnExpr("(?) AS pending_mention_count", pendingMentionsQuery(q.db, identity.User.ID).ColumnExpr("count(*)")).
		ColumnExpr("state.last_reviewed_mention_message_id AS reviewed_through_message_id").
		ColumnExpr("COALESCE(reviewed.message_seq, 0) AS reviewed_through_sequence").
		ColumnExpr("latest.id AS latest_message_id, COALESCE(latest.message_seq, 0) AS latest_sequence").
		Join("LEFT JOIN LATERAL (SELECT id, message_seq FROM messages WHERE organization_id = cv.organization_id AND conversation_id = cv.id AND deleted_at IS NULL ORDER BY message_seq DESC LIMIT 1) AS latest ON TRUE").
		Scan(ctx, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationNavigationState{}, ErrConversationNotFound
	}
	if err != nil {
		return ConversationNavigationState{}, fmt.Errorf("get conversation navigation state: %w", err)
	}
	return result, nil
}

// Execute 一次返回同一快照中的有序目标 ID 和本轮上界。
func (q *ListPendingConversationMentionsQuery) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (PendingConversationMentions, error) {
	if !common.ValidUUID(conversationID) {
		return PendingConversationMentions{}, &ValidationError{Fields: map[string]ValidationCode{"conversationId": ValidationConversationIDInvalid}}
	}
	var rows []struct {
		ID       *string
		Sequence *int64
	}
	err := mentionNavigationQuery(q.db, identity, conversationID).
		ColumnExpr("targets.id, targets.message_seq AS sequence").
		Join("LEFT JOIN LATERAL (?) AS targets ON TRUE", pendingMentionsQuery(q.db, identity.User.ID).ColumnExpr("pending.id, pending.message_seq")).
		OrderExpr("targets.message_seq ASC").Scan(ctx, &rows)
	if err != nil {
		return PendingConversationMentions{}, fmt.Errorf("list pending conversation mentions: %w", err)
	}
	if len(rows) == 0 {
		return PendingConversationMentions{}, ErrConversationNotFound
	}
	result := PendingConversationMentions{MessageIDs: []string{}}
	for _, row := range rows {
		if row.ID != nil {
			result.MessageIDs = append(result.MessageIDs, *row.ID)
			result.LastTargetSequence = row.Sequence
		}
	}
	return result, nil
}

// Execute 在会话锁内确认实际查看的提及，并合并连续查看水位。
func (a *MarkConversationMentionReviewedAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID, messageID string) (ConversationMentionReview, error) {
	if !common.ValidUUID(conversationID) || !common.ValidUUID(messageID) {
		return ConversationMentionReview{}, &ValidationError{Fields: map[string]ValidationCode{"messageId": ValidationLastReadMessageIDInvalid}}
	}
	var result ConversationMentionReview
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := chatstate.LockConversation(ctx, tx, identity.Organization.ID, conversationID); err != nil {
			return err
		}
		readable, err := mentionNavigationQuery(tx, identity, conversationID).Exists(ctx)
		if err != nil {
			return err
		}
		if !readable {
			return ErrConversationNotFound
		}
		var target struct {
			Sequence int64
			Deleted  bool
			Eligible bool
			Reviewed bool
		}
		err = mentionNavigationQuery(tx, identity, conversationID).
			Join("JOIN messages AS target ON target.organization_id = cv.organization_id AND target.conversation_id = cv.id AND target.id = ?", messageID).
			Join("JOIN conversation_participants AS sender ON sender.organization_id = target.organization_id AND sender.conversation_id = target.conversation_id AND sender.id = target.sender_participant_id").
			ColumnExpr("target.message_seq AS sequence, target.deleted_at IS NOT NULL AS deleted").
			ColumnExpr("EXISTS (SELECT 1 FROM conversation_mention_reviews AS receipt WHERE receipt.organization_id = cv.organization_id AND receipt.conversation_id = cv.id AND receipt.user_id = ? AND receipt.message_id = target.id) AS reviewed", identity.User.ID).
			ColumnExpr("COALESCE(sender.subject_id <> mine.id AND (target.mention_all OR EXISTS (SELECT 1 FROM message_mentions AS mention WHERE mention.organization_id = target.organization_id AND mention.message_id = target.id AND mention.subject_id = mine.id)), FALSE) AS eligible").Scan(ctx, &target)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMentionTargetInvalid
		}
		if err != nil {
			return err
		}
		if !target.Eligible {
			return ErrMentionTargetInvalid
		}
		if err := mentionNavigationQuery(tx, identity, conversationID).
			ColumnExpr("state.last_reviewed_mention_message_id AS reviewed_through_message_id, COALESCE(reviewed.message_seq, 0) AS reviewed_through_sequence").Scan(ctx, &result); err != nil {
			return err
		}
		if target.Sequence <= result.ReviewedThroughSequence || target.Reviewed {
			result.Outcome = "alreadyReviewed"
			return nil
		}
		if target.Deleted {
			result.Outcome = "unavailable"
			return nil
		}
		receipt := &servermodels.ConversationMentionReview{OrganizationID: identity.Organization.ID, ConversationID: conversationID, UserID: identity.User.ID, MessageID: messageID}
		if _, err := tx.NewInsert().Model(receipt).Column("organization_id", "conversation_id", "user_id", "message_id").Exec(ctx); err != nil {
			return err
		}
		if err := advanceMentionReviewState(ctx, tx, identity, conversationID, &result); err != nil {
			return err
		}
		// 每次确认提及推进一次个人状态版本。
		state := &servermodels.ConversationUserState{OrganizationID: identity.Organization.ID, ConversationID: conversationID, UserID: identity.User.ID, Version: 1}
		if err := notifyConversationStateWrite(ctx, tx.NewInsert().Model(state).Column("organization_id", "conversation_id", "user_id", "version").
			On("CONFLICT (organization_id, conversation_id, user_id) DO UPDATE").Set("version = cus.version + 1").Set("updated_at = now()").
			Returning("version"), identity.Organization.ID, conversationID, identity.User.ID); err != nil {
			return err
		}
		result.Outcome = "reviewed"
		return nil
	})
	if err != nil {
		return ConversationMentionReview{}, fmt.Errorf("mark conversation mention reviewed: %w", err)
	}
	return result, nil
}

// advanceMentionReviewState 把未跳过待查看提及的单条记录合并到连续水位。
func advanceMentionReviewState(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, conversationID string, result *ConversationMentionReview) error {
	var firstPending *int64
	if err := mentionNavigationQuery(tx, identity, conversationID).
		ColumnExpr("(?)", pendingMentionsQuery(tx, identity.User.ID).ColumnExpr("min(pending.message_seq)")).Scan(ctx, &firstPending); err != nil {
		return err
	}
	var latest struct {
		ID       string
		Sequence int64
	}
	query := tx.NewSelect().TableExpr("conversation_mention_reviews AS receipt").
		Join("JOIN messages AS msg ON msg.organization_id = receipt.organization_id AND msg.conversation_id = receipt.conversation_id AND msg.id = receipt.message_id").
		ColumnExpr("msg.id, msg.message_seq AS sequence").
		Where("receipt.organization_id = ? AND receipt.conversation_id = ? AND receipt.user_id = ?", identity.Organization.ID, conversationID, identity.User.ID).
		OrderExpr("msg.message_seq DESC").Limit(1)
	if firstPending != nil {
		query.Where("msg.message_seq < ?", *firstPending)
	}
	if err := query.Scan(ctx, &latest); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	state := &servermodels.ConversationUserState{OrganizationID: identity.Organization.ID, ConversationID: conversationID, UserID: identity.User.ID, LastReviewedMentionMessageID: &latest.ID}
	if _, err := tx.NewInsert().Model(state).Column("organization_id", "conversation_id", "user_id", "last_reviewed_mention_message_id").
		On("CONFLICT (organization_id, conversation_id, user_id) DO UPDATE").Set("last_reviewed_mention_message_id = EXCLUDED.last_reviewed_mention_message_id").Set("updated_at = now()").Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewDelete().Model((*servermodels.ConversationMentionReview)(nil)).
		Where("organization_id = ? AND conversation_id = ? AND user_id = ?", identity.Organization.ID, conversationID, identity.User.ID).
		Where("message_id IN (SELECT id FROM messages WHERE organization_id = ? AND conversation_id = ? AND message_seq <= ?)", identity.Organization.ID, conversationID, latest.Sequence).Exec(ctx); err != nil {
		return err
	}
	result.ReviewedThroughMessageID = &latest.ID
	result.ReviewedThroughSequence = latest.Sequence
	return nil
}
