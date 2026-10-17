//go:build server

package processquery

import (
	"context"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ConversationGuard 校验成员对会话的阅读资格；由装配方注入，避免本包依赖 conversationaccess。
// 不可读或不存在时须返回 chatstate.ErrConversationNotFound，供调用方映射为过程不可用错误。
type ConversationGuard interface {
	RequireReadable(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID string) error
}
