//go:build server

package integrationtest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/telegram"
	models "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
	f := newCustomerDeliveryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// 新周期路由到工作中的群主，路由锁定查询可在查询记录中识别。
	if _, err := f.db.ExecContext(ctx, "UPDATE organization_identities SET work_status = ?, handles_service_requests = true WHERE id = ?", domain.WorkStatusWorking, f.owner.OrganizationIdentity.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE channels SET initial_routing_target_type = ?, initial_routing_target_id = ? WHERE id = ?", domain.ChannelRoutingTargetTypeMember, f.owner.OrganizationIdentity.ID, f.channelID); err != nil {
		t.Fatal(err)
	}
	hook := &inboundRaceHook{close: func() {
		if _, err := f.db.ExecContext(context.Background(), "UPDATE service_sessions SET status = ?, closed_at = now() WHERE conversation_id = ? AND status = ?",
			domain.ServiceSessionStatusClosed, f.conversationID, domain.ServiceSessionStatusOpen); err != nil {
			t.Error(err)
		}
	}}
	f.db.AddQueryHook(hook)
	receiver := customerchataction.NewReceiveTelegramWebhookAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), domain.FileStorageBackendLocal, newTestTasks(f.db))
	if err := receiver.Execute(context.WithValue(ctx, inboundRaceKey{}, true), f.channelID, customerchataction.TelegramWebhookInput{
		Secret: "secret", UpdateID: 2,
		Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 2, DisplayName: "Telegram 客户", Body: "周期关闭后的消息", OriginatedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	var open []models.ServiceSession
	if err := f.db.NewSelect().Model(&open).Where("ss.conversation_id = ? AND ss.status = ?", f.conversationID, domain.ServiceSessionStatusOpen).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].AssigneeIdentityID == nil || *open[0].AssigneeIdentityID != f.owner.OrganizationIdentity.ID {
		t.Fatalf("open sessions = %+v", open)
	}
	// 回滚之后先对路由目标取共享锁，再锁定渠道身份。
	hook.mu.Lock()
	queries := slices.Clone(hook.queries)
	hook.mu.Unlock()
	rollback := slices.IndexFunc(queries, func(query string) bool { return strings.Contains(query, "ROLLBACK TO SAVEPOINT") })
	if rollback < 0 {
		t.Fatal("inbound did not roll back to savepoint")
	}
	redo := queries[rollback+1:]
	routeLock := slices.IndexFunc(redo, func(query string) bool {
		return strings.Contains(query, `"organization_identities"`) && strings.Contains(query, "KEY SHARE")
	})
	identityLock := slices.IndexFunc(redo, func(query string) bool {
		return strings.Contains(query, "contact_channel_identities") && strings.Contains(query, "FOR UPDATE")
	})
	if routeLock < 0 || identityLock < 0 || routeLock > identityLock {
		t.Fatalf("route lock at %d, identity lock at %d", routeLock, identityLock)
	}
}
