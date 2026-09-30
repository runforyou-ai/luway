//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// TestChatMessageAppendReplay 验证共享追加返回既有事实，重放不消耗群序号或回退摘要。
func TestChatMessageAppendReplay(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	input := groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "第一条"}
	first, err := newGroupSendAction(f.db).Execute(ctx, f.owner, input)
	if err != nil {
		t.Fatal(err)
	}
	latest := f.send(t, f.owner, "最新消息", false)
	before := &servermodels.Conversation{ID: f.groupID}
	if err := f.db.NewSelect().Model(before).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	err = realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, f.owner); err != nil {
			return err
		}
		member, err := chatstate.LockGroup(ctx, tx, f.owner, f.groupID, chatstate.GroupSendable)
		if err != nil {
			return err
		}
		key := "mmsg:" + f.owner.OrganizationIdentity.ID + ":" + input.ClientMessageID
		message, inserted, err := chatstate.AppendMessage(ctx, tx, member.Conversation, &servermodels.Message{
			ID: uuid.NewV7().String(), OrganizationID: f.owner.Organization.ID, ConversationID: f.groupID,
			SenderParticipantID: &member.ParticipantID, Type: string(domain.MessageTypeText), Body: input.Body,
			IdempotencyKey: &key, OriginatedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		if inserted || message.ID != first.ID || !message.CreatedAt.Equal(first.CreatedAt) || message.MessageSeq != first.MessageSeq {
			t.Fatalf("replayed message=%+v inserted=%v", message, inserted)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after := &servermodels.Conversation{ID: f.groupID}
	if err := f.db.NewSelect().Model(after).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if after.LastMessageID == nil || *after.LastMessageID != latest.ID || after.LastMessageSeq != before.LastMessageSeq || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("replay changed summary: before=%+v after=%+v", before, after)
	}
	if count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", f.groupID).Count(ctx); err != nil || count != 2 {
		t.Fatalf("message count=%d err=%v", count, err)
	}
}

// TestTelegramAppendUsesLocalSequence 验证晚到渠道消息保留来源时间，并按本地顺序推进会话和周期摘要。
func TestTelegramAppendUsesLocalSequence(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	before := &servermodels.Conversation{ID: f.conversationID}
	if err := f.db.NewSelect().Model(before).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	input := customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: 2, Message: &telegram.InboundMessage{
		ChatID: 12345, SenderID: 12345, MessageID: 2, DisplayName: "Telegram 客户", Body: "晚到的旧消息", OriginatedAt: before.LastMessageAt.Add(-time.Hour),
	}}
	receive := customerchataction.NewReceiveTelegramWebhookAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), domain.FileStorageBackendLocal, newTestTasks(f.db))
	for range 2 {
		if err := receive.Execute(ctx, f.channelID, input); err != nil {
			t.Fatal(err)
		}
	}
	var messages []servermodels.Message
	if err := f.db.NewSelect().Model(&messages).Where("conversation_id = ? AND body = ?", f.conversationID, input.Message.Body).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || !messages[0].OriginatedAt.Equal(input.Message.OriginatedAt) || messages[0].SourceOrder != 2 {
		t.Fatalf("late message=%+v", messages)
	}
	after := &servermodels.Conversation{ID: f.conversationID}
	if err := f.db.NewSelect().Model(after).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if after.LastMessageID == nil || *after.LastMessageID != messages[0].ID || after.LastMessageSeq != before.LastMessageSeq+1 || messages[0].MessageSeq != after.LastMessageSeq {
		t.Fatalf("late message did not advance summary: before=%+v after=%+v", before, after)
	}
	assertCustomerLockSummary(t, ctx, f.db, f.conversationID)
}

// testWebsiteAppendRollback 验证访客首发的消息、双摘要、周期、Trigger 和真实任务唤醒一起回滚。
func testWebsiteAppendRollback(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID string, tasks *servertask.Runtime) {
	t.Helper()
	ctx := context.Background()
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "访客消息回滚", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agentID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	failing := &failingMessageScheduler{inner: agentrunaction.NewScheduler(tasks), failure: errors.New("rollback visitor input")}
	_, err = customerchataction.NewReceiveWebsiteCustomerMessageAction(db, failing, newTestTasks(db), nil).Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "访客首发",
	})
	if !errors.Is(err, failing.failure) || len(failing.taskIDs) != 1 || failing.conversationID == "" {
		t.Fatalf("atomic visitor rows=%v conversation=%s err=%v", failing.taskIDs, failing.conversationID, err)
	}
	for table, column := range map[string]string{
		"conversations": "id", "channel_conversations": "conversation_id", "service_sessions": "conversation_id",
		"messages": "conversation_id", "conversation_participants": "conversation_id", "agent_lanes": "conversation_id",
		"agent_runs": "conversation_id",
	} {
		count, err := db.NewSelect().TableExpr(table).Where("? = ?", bun.Ident(column), failing.conversationID).Count(ctx)
		if err != nil || count != 0 {
			t.Fatalf("rollback %s rows=%d err=%v", table, count, err)
		}
	}
	inputCount, err := db.NewSelect().TableExpr("agent_inputs AS ai").
		Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").
		Where("al.conversation_id = ?", failing.conversationID).Count(ctx)
	if err != nil || inputCount != 0 {
		t.Fatalf("rollback agent_inputs rows=%d err=%v", inputCount, err)
	}
	for table, column := range map[string]string{"task_runs": "id", "task_outbox": "task_run_id"} {
		count, err := db.NewSelect().TableExpr(table).Where("? IN (?)", bun.Ident(column), bun.In(failing.taskIDs)).Count(ctx)
		if err != nil || count != 0 {
			t.Fatalf("rollback %s rows=%d err=%v", table, count, err)
		}
	}
}

// TestGroupSystemMessageSummary 验证群资料变更生成系统消息并推进摘要，重复保存不生成新消息。
func TestGroupSystemMessageSummary(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	first := f.send(t, f.owner, "改名前的消息", false)
	update := groupchataction.NewUpdateGroupConversationAction(f.db)
	input := groupchataction.GroupConversationProfileInput{ConversationID: f.groupID, Title: "改名后的群"}
	for range 2 {
		if _, err := update.Execute(ctx, f.owner, input); err != nil {
			t.Fatal(err)
		}
	}
	var messages []servermodels.Message
	if err := f.db.NewSelect().Model(&messages).Where("conversation_id = ?", f.groupID).OrderExpr("message_seq").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Type != string(domain.MessageTypeSystem) || messages[1].SystemEventType == nil || *messages[1].SystemEventType != string(domain.ConversationSystemEventGroupRenamed) {
		t.Fatalf("group system messages=%+v", messages)
	}
	var cv servermodels.Conversation
	if err := f.db.NewSelect().Model(&cv).Where("id = ?", f.groupID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if cv.LastMessageID == nil || *cv.LastMessageID != messages[1].ID || cv.LastMessageSeq != first.MessageSeq+1 {
		t.Fatalf("system summary=%+v", cv)
	}
}

// appendTestMessage 在会话锁内通过共用追加入口创建引用边界夹具。
func appendTestMessage(t *testing.T, db *bun.DB, message *servermodels.Message) {
	t.Helper()
	message.ID = uuid.NewV7().String()
	err := realtime.RunInTx(context.Background(), db, func(ctx context.Context, tx bun.Tx) error {
		cv := &servermodels.Conversation{ID: message.ConversationID}
		if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		_, _, err := chatstate.AppendMessage(ctx, tx, cv, message)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
