//go:build server

package integrationtest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// inboundRaceKey 标记需要记录查询并在会话加锁前关闭周期的入站上下文。
type inboundRaceKey struct{}

// inboundRaceHook 记录入站事务的查询顺序，并在首次锁定会话前关闭当前周期。
type inboundRaceHook struct {
	mu      sync.Mutex
	queries []string
	once    sync.Once
	close   func()
}

// BeforeQuery 记录带标记上下文的查询，首次锁定会话前执行关闭。
func (h *inboundRaceHook) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if ctx.Value(inboundRaceKey{}) == nil {
		return ctx
	}
	h.mu.Lock()
	h.queries = append(h.queries, event.Query)
	h.mu.Unlock()
	if strings.Contains(event.Query, `FROM "conversations"`) && strings.Contains(event.Query, "FOR UPDATE") {
		h.once.Do(h.close)
	}
	return ctx
}

// AfterQuery 不做处理。
func (h *inboundRaceHook) AfterQuery(context.Context, *bun.QueryEvent) {}

// TestInboundRedoesWhenSessionClosedAfterCheck 验证预判存在进行中周期、加锁前周期被关闭时，入站回滚到保存点并先锁定路由目标再重做，只开启一个新周期。
func TestInboundRedoesWhenSessionClosedAfterCheck(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// 新周期路由到工作中的群主，路由锁定查询可在查询记录中识别。
	_, err := f.db.ExecContext(ctx, "UPDATE workspace_identities SET work_status = ?, handles_service_requests = true WHERE id = ?", domain.WorkStatusWorking, f.owner.WorkspaceIdentity.ID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(ctx, "UPDATE channels SET initial_routing_target_type = ?, initial_routing_target_id = ? WHERE id = ?", domain.ChannelRoutingTargetTypeMember, f.owner.WorkspaceIdentity.ID, f.channelID)
	require.NoError(t, err)
	hook := &inboundRaceHook{close: func() {
		_, err := f.db.ExecContext(context.Background(), "UPDATE service_sessions SET status = ?, closed_at = now() WHERE conversation_id = ? AND status = ?",
			domain.ServiceSessionStatusClosed, f.conversationID, domain.ServiceSessionStatusOpen)
		assert.NoError(t, err)
	}}
	f.db.AddQueryHook(hook)
	receiver := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	require.NoError(t, receiver.Execute(context.WithValue(ctx, inboundRaceKey{}, true), f.channelID, telegramUpdate{
		Secret: "secret", UpdateID: 2,
		Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 2, DisplayName: "Telegram 客户", Body: "周期关闭后的消息", OriginatedAt: time.Now().UTC()},
	}))
	var open []models.ServiceSession
	require.NoError(t, f.db.NewSelect().Model(&open).Where("ss.conversation_id = ? AND ss.status = ?", f.conversationID, domain.ServiceSessionStatusOpen).Scan(ctx))
	require.Len(t, open, 1)
	require.NotNil(t, open[0].AssigneeIdentityID)
	require.Equal(t, f.owner.WorkspaceIdentity.ID, *open[0].AssigneeIdentityID)
	// 回滚之后先对路由目标取共享锁，再锁定渠道身份。
	hook.mu.Lock()
	queries := slices.Clone(hook.queries)
	hook.mu.Unlock()
	rollback := slices.IndexFunc(queries, func(query string) bool { return strings.Contains(query, "ROLLBACK TO SAVEPOINT") })
	require.GreaterOrEqual(t, rollback, 0, "inbound did not roll back to savepoint")
	redo := queries[rollback+1:]
	routeLock := slices.IndexFunc(redo, func(query string) bool {
		return strings.Contains(query, `"workspace_identities"`) && strings.Contains(query, "KEY SHARE")
	})
	identityLock := slices.IndexFunc(redo, func(query string) bool {
		return strings.Contains(query, "channel_identities") && strings.Contains(query, "FOR UPDATE")
	})
	require.GreaterOrEqual(t, routeLock, 0)
	require.GreaterOrEqual(t, identityLock, 0)
	require.LessOrEqual(t, routeLock, identityLock)
}
