//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
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
	var result ConversationReadState
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 取得会话共享锁后复核本人管理资格。
		if err := conversationaccess.LockManageable(ctx, tx, identity, conversationID); err != nil {
			return err
		}
		var target servermodels.Message
		err := tx.NewSelect().Model(&target).Column("msg.message_seq").
			Where("msg.workspace_id = ?", identity.Workspace.ID).
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
			WorkspaceID: identity.Workspace.ID, ConversationID: conversationID,
			UserID: identity.User.ID, LastReadMessageID: &messageID,
		}
		if err := AdvanceConversationUserReadState(ctx, tx, state, &target); err != nil {
			return err
		}
		ownRow := ownState(tx, identity, conversationID)
		// 主动标为已读时，在同一事务清除独立标记；可见消息自动已读只推进水位。
		if clearUnreadMark {
			if err := ownRow.update(ctx, "marked_unread", "marked_unread = false"); err != nil {
				return fmt.Errorf("clear conversation unread mark: %w", err)
			}
		}
		if err := tx.NewSelect().Model((*servermodels.ConversationUserState)(nil)).
			ColumnExpr("cus.read_seq, cus.last_read_message_id, cus.last_read_at").
			ApplyQueryBuilder(ownRow.key.Scope).
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
	state.ReadSeq = message.MessageSeq
	state.Version = 1
	if err := notifyConversationStateWrite(ctx, db.NewInsert().Model(state).
		Column("workspace_id", "conversation_id", "user_id", "last_read_message_id", "last_read_at", "read_seq", "version").
		Value("last_read_at", "now()").
		On("CONFLICT (workspace_id, conversation_id, user_id) DO UPDATE").
		Set("last_read_message_id = EXCLUDED.last_read_message_id").
		Set("read_seq = EXCLUDED.read_seq").
		Set("version = cus.version + 1").
		Set("last_read_at = now()").
		Where("cus.read_seq < EXCLUDED.read_seq").
		Returning("version"), state.WorkspaceID, state.ConversationID, state.UserID); err != nil {
		return fmt.Errorf("advance conversation user read state: %w", err)
	}
	return nil
}

// notifyConversationStateWrite 执行返回个人状态版本的写入，写入生效时登记本人会话状态通知。
func notifyConversationStateWrite(ctx context.Context, query interface {
	Scan(context.Context, ...any) error
}, workspaceID, conversationID, userID string) error {
	var version int64
	if err := query.Scan(ctx, &version); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	// 版本 0 与缺少个人状态行等价，不登记通知。
	if version > 0 {
		realtime.Notify(ctx, realtime.UserConversationStateChanged(workspaceID, userID, conversationID, version))
	}
	return nil
}
