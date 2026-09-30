//go:build server

package realtime

import (
	"context"
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestNotifyMergesConversationChanges 验证同一事务内同一受众与会话的变更通知合并为最高版本，并保留全部变化类别。
func TestNotifyMergesConversationChanges(t *testing.T) {
	pending := &batch{items: map[mergeKey]Notification{}}
	ctx := context.WithValue(context.Background(), batchKey{}, pending)
	Notify(ctx, UserConversationChanged("organization", "user", "conversation", domain.ConversationTypeGroup, 5, domain.ConversationChangeTimeline))
	Notify(ctx, UserConversationChanged("organization", "user", "conversation", domain.ConversationTypeGroup, 4, domain.ConversationChangeParticipants))
	Notify(ctx, UserConversationChanged("organization", "user", "conversation", domain.ConversationTypeGroup, 6, domain.ConversationChangeService))
	if len(pending.order) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending.order))
	}
	got := pending.items[pending.order[0]]
	want := domain.ConversationChangeTimeline | domain.ConversationChangeService | domain.ConversationChangeParticipants
	if got.Version != 6 || got.Changes != want {
		t.Fatalf("merged = version %d changes %d, want version 6 changes %d", got.Version, got.Changes, want)
	}
}
