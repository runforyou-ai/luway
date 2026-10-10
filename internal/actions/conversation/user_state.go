//go:build server

package conversation

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// userStateRow 是当前成员在一个会话中的个人状态行，写入使版本推进时登记本人会话状态通知。
type userStateRow struct {
	tx  bun.Tx
	key servermodels.ConversationUserStateKey
}

// ownState 返回当前成员在会话中的个人状态行。
func ownState(tx bun.Tx, identity *servermodels.Identity, conversationID string) userStateRow {
	return userStateRow{tx: tx, key: servermodels.ConversationUserStateKey{
		WorkspaceID: identity.Workspace.ID, ConversationID: conversationID, UserID: identity.User.ID,
	}}
}

// ensure 状态行不存在时以版本 0 建立，版本 0 与缺少状态行等价。
func (r userStateRow) ensure(ctx context.Context) error {
	if _, err := r.tx.NewInsert().Model(&servermodels.ConversationUserState{
		WorkspaceID: r.key.WorkspaceID, ConversationID: r.key.ConversationID, UserID: r.key.UserID,
	}).Column("workspace_id", "conversation_id", "user_id", "version").
		On("CONFLICT (workspace_id, conversation_id, user_id) DO NOTHING").Exec(ctx); err != nil {
		return fmt.Errorf("create conversation user state: %w", err)
	}
	return nil
}

// update 在 condition 成立时执行 sets 并推进版本；状态行不存在时不写入。
func (r userStateRow) update(ctx context.Context, condition string, sets ...string) error {
	query := r.tx.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
		ApplyQueryBuilder(r.key.Scope).Where(condition).Set("version = cus.version + 1").Returning("version")
	for _, set := range sets {
		query.Set(set)
	}
	return notifyConversationStateWrite(ctx, query, r.key.WorkspaceID, r.key.ConversationID, r.key.UserID)
}

// upsert 状态行不存在时按 row 的 columns 与版本插入，已存在且 condition 成立时执行 sets 并推进版本；condition 以 cus 引用已有行、EXCLUDED 引用 row。
func (r userStateRow) upsert(ctx context.Context, row *servermodels.ConversationUserState, columns []string, condition string, sets ...string) error {
	row.WorkspaceID, row.ConversationID, row.UserID = r.key.WorkspaceID, r.key.ConversationID, r.key.UserID
	query := r.tx.NewInsert().Model(row).
		Column(append([]string{"workspace_id", "conversation_id", "user_id", "version"}, columns...)...).
		On("CONFLICT (workspace_id, conversation_id, user_id) DO UPDATE").
		Set("version = cus.version + 1").Where(condition).Returning("version")
	for _, set := range sets {
		query.Set(set)
	}
	return notifyConversationStateWrite(ctx, query, r.key.WorkspaceID, r.key.ConversationID, r.key.UserID)
}
