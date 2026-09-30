//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ConversationReadState 表示用户会话的已读水位。
type ConversationReadState struct {
	ReadSeq           int64
	LastReadMessageID string
	LastReadAt        time.Time
}

// MarkConversationReadAction 单调推进用户会话已读水位。
type MarkConversationReadAction struct{ db *bun.DB }

// NewMarkConversationReadAction 创建用户会话标记已读操作。
func NewMarkConversationReadAction(db *bun.DB) *MarkConversationReadAction {
	return &MarkConversationReadAction{db: db}
}

// Execute 校验会话访问权并单调推进已读消息水位。
func (a *MarkConversationReadAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID, messageID string, clearUnreadMark bool) (ConversationReadState, error) {
	fields := make(map[string]ValidationCode)
	if !common.ValidUUID(conversationID) {
		fields["conversationId"] = ValidationConversationIDInvalid
	}
	if !common.ValidUUID(messageID) {
		fields["lastReadMessageId"] = ValidationLastReadMessageIDInvalid
	}
	if len(fields) > 0 {
		return ConversationReadState{}, &ValidationError{Fields: fields}
	}
	var result ConversationReadState
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var conversationType domain.ConversationType
		err := tx.NewSelect().TableExpr("conversations AS cv").Column("cv.type").
			Where("cv.organization_id = ? AND cv.id = ?", identity.Organization.ID, conversationID).Scan(ctx, &conversationType)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConversationNotFound
		}
		if err != nil {
			return fmt.Errorf("load conversation read type: %w", err)
		}
		// 承载服务会话的会话对企业成员开放阅读，渠道会话按历史访问范围校验，其余会话要求在场成员。
		served, err := tx.NewSelect().Model((*servermodels.ServiceConversation)(nil)).
			Where("svc.organization_id = ? AND svc.conversation_id = ?", identity.Organization.ID, conversationID).Exists(ctx)
		if err != nil {
			return fmt.Errorf("check service conversation read access: %w", err)
		}
		if !served {
			if conversationType == domain.ConversationTypeChannel {
				if err := AuthorizeConversationHistory(ctx, tx, identity, conversationID); err != nil {
					return err
				}
			} else if _, err := chatstate.LockMember(ctx, tx, identity, conversationID); err != nil {
				return err
			}
		}
		var target servermodels.Message
		err = tx.NewSelect().Model(&target).Column("msg.message_seq").
			Where("msg.organization_id = ?", identity.Organization.ID).
			Where("msg.conversation_id = ?", conversationID).
			Where("msg.id = ?", messageID).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConversationNotFound
		}
		if err != nil {
			return fmt.Errorf("load conversation read target: %w", err)
		}

		state := &servermodels.ConversationUserState{
			OrganizationID: identity.Organization.ID, ConversationID: conversationID,
			UserID: identity.User.ID, LastReadMessageID: &messageID,
		}
		if err := AdvanceConversationUserReadState(ctx, tx, state, &target); err != nil {
			return err
		}
		// 主动标为已读时，在同一事务清除独立标记；可见消息自动已读只推进水位。
		if clearUnreadMark {
			if err := notifyConversationStateWrite(ctx, tx.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
				Set("marked_unread = false").Set("version = version + 1").Set("updated_at = now()").
				Where("organization_id = ? AND conversation_id = ? AND user_id = ? AND marked_unread", identity.Organization.ID, conversationID, identity.User.ID).
				Returning("version"), identity.Organization.ID, conversationID, identity.User.ID); err != nil {
				return fmt.Errorf("clear conversation unread mark: %w", err)
			}
		}
		if err := tx.NewSelect().
			TableExpr("conversation_user_states AS cus").
			ColumnExpr("cus.read_seq, cus.last_read_message_id, cus.last_read_at").
			Where("cus.organization_id = ?", identity.Organization.ID).
			Where("cus.conversation_id = ?", conversationID).
			Where("cus.user_id = ?", identity.User.ID).
			Scan(ctx, &result); err != nil {
			return fmt.Errorf("load current conversation read state: %w", err)
		}
		return nil
	})
	if err != nil {
		return ConversationReadState{}, fmt.Errorf("mark conversation read: %w", err)
	}
	return result, nil
}

// AdvanceConversationUserReadState 按消息稳定顺序单调推进用户已读水位。
func AdvanceConversationUserReadState(ctx context.Context, db bun.IDB, state *servermodels.ConversationUserState, message *servermodels.Message) error {
	readAt := time.Now().UTC()
	state.LastReadAt = &readAt
	state.ReadSeq = message.MessageSeq
	state.Version = 1
	if err := notifyConversationStateWrite(ctx, db.NewInsert().Model(state).
		Column("organization_id", "conversation_id", "user_id", "last_read_message_id", "last_read_at", "read_seq", "version").
		On("CONFLICT (organization_id, conversation_id, user_id) DO UPDATE").
		Set("last_read_message_id = EXCLUDED.last_read_message_id").
		Set("read_seq = EXCLUDED.read_seq").
		Set("version = cus.version + 1").
		Set("last_read_at = now()").
		Set("updated_at = now()").
		Where("cus.read_seq < EXCLUDED.read_seq").
		Returning("version"), state.OrganizationID, state.ConversationID, state.UserID); err != nil {
		return fmt.Errorf("advance conversation user read state: %w", err)
	}
	return nil
}

// notifyConversationStateWrite 执行返回个人状态版本的写入，写入生效时登记本人会话状态通知。
func notifyConversationStateWrite(ctx context.Context, query interface {
	Scan(context.Context, ...any) error
}, organizationID, conversationID, userID string) error {
	var version int64
	if err := query.Scan(ctx, &version); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	// 版本 0 与缺少个人状态行等价，不登记通知。
	if version > 0 {
		realtime.Notify(ctx, realtime.UserConversationStateChanged(organizationID, userID, conversationID, version))
	}
	return nil
}
