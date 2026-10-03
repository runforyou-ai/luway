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
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

type customerReadFixture struct {
	navigationFixture
	channelID, conversationID string
	receive                   *customerchataction.ReceiveWebsiteCustomerMessageAction
}

// newCustomerReadFixture 建立两个客服共享的网站客户会话，渠道开启多会话。
func newCustomerReadFixture(t *testing.T) customerReadFixture {
	t.Helper()
	f := newNavigationFixture(t)
	ctx := context.Background()
	var roleID string
	if err := f.db.NewSelect().Table("roles").Column("id").Where("organization_id = ? AND kind = ?", f.owner.Organization.ID, domain.RoleKindCustomerService).Scan(ctx, &roleID); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []*servermodels.Identity{f.owner, f.member} {
		if _, err := f.db.NewUpdate().Table("users").Set("role_id = ?", roleID).Where("organization_id = ? AND id = ?", identity.Organization.ID, identity.User.ID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		identity.User.RoleID = roleID
	}
	channel, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "客服未读测试", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	setWebsiteChannelSetting(t, f.db, channel.ID, "multiple_conversations_enabled", true)
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), newTestTasks(f.db), nil)
	result, err := receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "客户首条消息",
	})
	if err != nil {
		t.Fatal(err)
	}
	return customerReadFixture{navigationFixture: f, channelID: channel.ID, conversationID: result.Conversation.ID, receive: receive}
}

// inboxRow 读取全部服务会话中指定服务状态的目标行，并核对内部提醒总数没有被客户消息改变。
func (f customerReadFixture) inboxRow(t *testing.T, identity *servermodels.Identity, status domain.ServiceSessionStatus) inboxaction.ConversationSummary {
	t.Helper()
	rowsPage, counts, err := inboxaction.NewLoadInboxQuery(f.db).Execute(context.Background(), identity, inboxaction.LoadInput{Scope: domain.InboxScopeAll, ServiceStatus: status})
	rows := rowsPage.Conversations
	if err != nil {
		t.Fatal(err)
	}
	if counts.Unread != 0 || counts.Attention != 0 {
		t.Fatalf("customer changed internal counts: %+v", counts)
	}
	for _, row := range rows {
		if row.ID == f.conversationID {
			return row
		}
	}
	t.Fatalf("customer conversation missing: %+v", rows)
	return inboxaction.ConversationSummary{}
}

// visitorMessage 通过网站入口向已有客服会话发送消息。
func (f customerReadFixture) visitorMessage(ctx context.Context, body string) (customerchataction.ReceiveWebsiteCustomerMessageResult, error) {
	return f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), Body: body,
	})
}

// TestCustomerConversationPersonalRead 验证独立阅读、自己的回复及跨处理周期的水位。
func TestCustomerConversationPersonalRead(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	read := conversationaction.NewMarkConversationReadAction(f.db)
	first := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen)
	if first.UnreadCount != 1 || first.LastReadMessageID != nil {
		t.Fatalf("initial unread: %+v", first)
	}
	if _, err := read.Execute(ctx, f.owner, f.conversationID, *first.LastMessageID, true); err != nil {
		t.Fatal(err)
	}
	if row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen); row.UnreadCount != 0 {
		t.Fatalf("owner unread: %+v", row)
	}
	if row := f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen); row.UnreadCount != 1 {
		t.Fatalf("reading changed coworker: %+v", row)
	}
	// 核验旁观客服阅读后参与关系和队列归属保持原值。
	count, err := f.db.NewSelect().Table("conversation_participants").Where("conversation_id = ?", f.conversationID).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("reading created participant: %d %v", count, err)
	}
	reply, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "客服回复"})
	if err != nil {
		t.Fatal(err)
	}
	if row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen); row.UnreadCount != 0 {
		t.Fatalf("own reply unread: %+v", row)
	}
	if row := f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen); row.UnreadCount != 2 {
		t.Fatalf("coworker reply not counted: %+v", row)
	}
	for _, id := range []string{reply.ID, *first.LastMessageID, reply.ID} {
		state, err := read.Execute(ctx, f.member, f.conversationID, id, false)
		if err != nil || state.LastReadMessageID != reply.ID {
			t.Fatalf("monotonic read: %+v %v", state, err)
		}
	}
	coordinator := agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil)
	if _, err := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(newTestTasks(f.db)), newTestTasks(f.db)).Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	if row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen); row.LastReadMessageID == nil || *row.LastReadMessageID != *first.LastMessageID {
		t.Fatalf("transfer changed owner read: %+v", row)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.member, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if row := f.inboxRow(t, f.member, domain.ServiceSessionStatusClosed); row.UnreadCount != 0 || *row.LastReadMessageID != reply.ID {
		t.Fatalf("close changed read: %+v", row)
	}
	if _, err := servicesessionaction.NewReopenServiceSessionAction(f.db).Execute(ctx, f.member, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if row := f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen); *row.LastReadMessageID != reply.ID {
		t.Fatalf("reopen changed read: %+v", row)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.member, f.conversationID); err != nil {
		t.Fatal(err)
	}
	next, err := f.visitorMessage(ctx, "新的处理周期")
	if err != nil || !next.OpenedNewServiceSession {
		t.Fatalf("new session: %+v %v", next, err)
	}
	if row := f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen); row.UnreadCount != 1 || *row.LastReadMessageID != reply.ID {
		t.Fatalf("new session reset read: %+v", row)
	}
}

// TestCustomerConversationReadBoundaries 验证企业隔离、消息归属、删除和发送主体判断。
func TestCustomerConversationReadBoundaries(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	other := newCustomerReadFixture(t)
	ctx := context.Background()
	read := conversationaction.NewMarkConversationReadAction(f.db)
	first := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen)
	foreign := other.inboxRow(t, other.owner, domain.ServiceSessionStatusOpen)
	for _, attempt := range []struct {
		identity              *servermodels.Identity
		conversation, message string
	}{
		{other.owner, f.conversationID, *first.LastMessageID},
		{f.owner, f.conversationID, *foreign.LastMessageID},
		{f.owner, f.conversationID, uuid.NewV7().String()},
	} {
		if _, err := read.Execute(ctx, attempt.identity, attempt.conversation, attempt.message, true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("invalid read: %v", err)
		}
	}
	// 联系人与员工即使来源编号相同，也必须按主体类型区分。
	if _, err := f.db.NewUpdate().Table("chat_subjects").Set("source_id = ?", f.owner.OrganizationIdentity.ID).Where("organization_id = ? AND kind = ?", f.owner.Organization.ID, domain.ChatSubjectKindContact).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen); row.UnreadCount != 1 {
		t.Fatalf("contact mistaken for self: %+v", row)
	}
	if _, err := read.Execute(ctx, f.owner, f.conversationID, *first.LastMessageID, false); err != nil {
		t.Fatal(err)
	}
	second, err := f.visitorMessage(ctx, "会被删除的消息")
	if err != nil {
		t.Fatal(err)
	}
	third, err := f.visitorMessage(ctx, "保留的消息")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Table("messages").Set("deleted_at = now()").Where("id IN (?)", bun.In([]string{*first.LastMessageID, second.Message.ID})).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen); row.UnreadCount != 1 || *row.LastReadMessageID != *first.LastMessageID {
		t.Fatalf("deleted read anchor: %+v", row)
	}
	if _, err := read.Execute(ctx, f.owner, f.conversationID, third.Message.ID, true); err != nil {
		t.Fatal(err)
	}
	if row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen); row.UnreadCount != 0 {
		t.Fatalf("explicit read: %+v", row)
	}
}

// TestCustomerConversationDelayedMessage 验证等待其他锁的消息在后续提交时仍进入未读与增量历史。
func TestCustomerConversationDelayedMessage(t *testing.T) {
	t.Parallel()
	for _, visitor := range []bool{true, false} {
		name := "member"
		if visitor {
			name = "visitor"
		}
		t.Run(name, func(t *testing.T) {
			f := newCustomerReadFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var blockerPID int
			if err := tx.NewSelect().ColumnExpr("pg_backend_pid()").Scan(ctx, &blockerPID); err != nil {
				t.Fatal(err)
			}
			if visitor {
				_, err = tx.ExecContext(ctx, "SELECT id FROM contact_channel_identities WHERE channel_id = ? FOR UPDATE", f.channelID)
			} else {
				_, err = tx.ExecContext(ctx, "SELECT id FROM users WHERE id = ? FOR UPDATE", f.owner.User.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if visitor {
					_, err := f.visitorMessage(ctx, "等待后入站")
					done <- err
				} else {
					_, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "等待后回复"})
					done <- err
				}
			}()
			// 等到请求确实阻塞，再让另一条消息先提交并被阅读。
			for {
				var blocked bool
				if err := f.db.NewSelect().ColumnExpr("EXISTS (SELECT 1 FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid)))", blockerPID).Scan(ctx, &blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("message finished before lock: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			var earlierID string
			if visitor {
				reply, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "先提交回复"})
				if err != nil {
					t.Fatal(err)
				}
				earlierID = reply.ID
			} else {
				inbound, err := f.visitorMessage(ctx, "先提交入站")
				if err != nil {
					t.Fatal(err)
				}
				earlierID = inbound.Message.ID
			}
			if _, err := conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.member, f.conversationID, earlierID, false); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			row := f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen)
			if row.UnreadCount != 1 {
				t.Fatalf("late commit disappeared from unread: %+v", row)
			}
			var earlier servermodels.Message
			if err := f.db.NewSelect().Model(&earlier).Where("msg.id = ?", earlierID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID, After: &conversationaction.MessageCursorPoint{ID: earlier.ID, MessageSeq: earlier.MessageSeq}})
			// 延迟提交的成员回复同时领取周期，领取事件不计入对话消息。
			conversational := 0
			for _, message := range history.Messages {
				if message.Type != domain.MessageTypeSystem {
					conversational++
				}
			}
			if err != nil || conversational != 1 {
				t.Fatalf("late commit missing from after: %+v %v", history, err)
			}
		})
	}
}
