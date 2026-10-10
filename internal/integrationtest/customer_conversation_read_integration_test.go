//go:build server

package integrationtest

import (
	"context"
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
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// customerReadFixture 是两个客服共享的网站客户会话测试环境。
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
	require.NoError(t, f.db.NewSelect().Table("roles").Column("id").Where("workspace_id = ? AND kind = ?", f.owner.Workspace.ID, domain.RoleKindCustomerService).Scan(ctx, &roleID))
	for _, identity := range []*servermodels.Identity{f.owner, f.member} {
		_, err := f.db.NewUpdate().Table("users").Set("role_id = ?", roleID).Where("workspace_id = ? AND id = ?", identity.Workspace.ID, identity.User.ID).Exec(ctx)
		require.NoError(t, err)
		identity.User.RoleID = roleID
	}
	channel, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "客服未读测试", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	setWebsiteChannelSetting(t, f.db, channel.ID, "multiple_conversations_enabled", true)
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer, servertest.DisabledMail{})
	result, err := receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "客户首条消息",
	})
	require.NoError(t, err)
	return customerReadFixture{navigationFixture: f, channelID: channel.ID, conversationID: result.Conversation.ID, receive: receive}
}

// inboxRow 读取全部服务会话中指定服务状态的目标行，并核对内部提醒总数没有被客户消息改变。
func (f customerReadFixture) inboxRow(t *testing.T, identity *servermodels.Identity, status domain.ServiceSessionStatus) inboxaction.ConversationSummary {
	t.Helper()
	rowsPage, counts, err := inboxaction.NewLoadInboxQuery(f.db).Execute(context.Background(), identity, inboxaction.LoadInput{Scope: domain.InboxScopeAll, ServiceStatus: status})
	rows := rowsPage.Conversations
	require.NoError(t, err)
	require.Zero(t, counts.Unread, "customer changed internal counts")
	require.Zero(t, counts.Attention, "customer changed internal counts")
	for _, row := range rows {
		if row.ID == f.conversationID {
			return row
		}
	}
	require.Failf(t, "customer conversation missing", "%+v", rows)
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
	require.Equal(t, 1, first.UnreadCount)
	require.Nil(t, first.LastReadMessageID)
	_, err := read.Execute(ctx, f.owner, f.conversationID, *first.LastMessageID, true)
	require.NoError(t, err)
	require.Equal(t, 0, f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen).UnreadCount, "owner unread")
	require.Equal(t, 1, f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen).UnreadCount, "reading changed coworker")
	// 核验旁观客服阅读后参与关系和队列归属保持原值。
	count, err := f.db.NewSelect().Table("conversation_participants").Where("conversation_id = ?", f.conversationID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "reading created participant")
	reply, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "客服回复"})
	require.NoError(t, err)
	require.Equal(t, 0, f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen).UnreadCount, "own reply unread")
	require.Equal(t, 2, f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen).UnreadCount, "coworker reply not counted")
	for _, id := range []string{reply.ID, *first.LastMessageID, reply.ID} {
		state, err := read.Execute(ctx, f.member, f.conversationID, id, false)
		require.NoError(t, err)
		require.Equal(t, reply.ID, state.LastReadMessageID, "monotonic read")
	}
	coordinator := newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	_, err = servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer).Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen)
	require.NotNil(t, row.LastReadMessageID, "transfer changed owner read")
	require.Equal(t, *first.LastMessageID, *row.LastReadMessageID, "transfer changed owner read")
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	row = f.inboxRow(t, f.member, domain.ServiceSessionStatusClosed)
	require.Zero(t, row.UnreadCount, "close changed read")
	require.Equal(t, reply.ID, *row.LastReadMessageID, "close changed read")
	_, err = servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	require.Equal(t, reply.ID, *f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen).LastReadMessageID, "reopen changed read")
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	next, err := f.visitorMessage(ctx, "新的处理周期")
	require.NoError(t, err)
	require.True(t, next.OpenedNewServiceSession)
	row = f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen)
	require.Equal(t, 1, row.UnreadCount, "new session reset read")
	require.Equal(t, reply.ID, *row.LastReadMessageID, "new session reset read")
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
		_, err := read.Execute(ctx, attempt.identity, attempt.conversation, attempt.message, true)
		require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "invalid read")
	}
	// 联系人与员工即使来源编号相同，也必须按主体类型区分。
	_, err := f.db.NewUpdate().Table("chat_subjects").Set("source_id = ?", f.owner.WorkspaceIdentity.ID).Where("workspace_id = ? AND kind = ?", f.owner.Workspace.ID, domain.ChatSubjectKindContact).Exec(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen).UnreadCount, "contact mistaken for self")
	_, err = read.Execute(ctx, f.owner, f.conversationID, *first.LastMessageID, false)
	require.NoError(t, err)
	second, err := f.visitorMessage(ctx, "会被删除的消息")
	require.NoError(t, err)
	third, err := f.visitorMessage(ctx, "保留的消息")
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Table("messages").Set("deleted_at = now()").Where("id IN (?)", bun.List([]string{*first.LastMessageID, second.Message.ID})).Exec(ctx)
	require.NoError(t, err)
	row := f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen)
	require.Equal(t, 1, row.UnreadCount, "deleted read anchor")
	require.Equal(t, *first.LastMessageID, *row.LastReadMessageID, "deleted read anchor")
	_, err = read.Execute(ctx, f.owner, f.conversationID, third.Message.ID, true)
	require.NoError(t, err)
	require.Equal(t, 0, f.inboxRow(t, f.owner, domain.ServiceSessionStatusOpen).UnreadCount, "explicit read")
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
			require.NoError(t, err)
			defer tx.Rollback()
			var blockerPID int
			require.NoError(t, tx.NewSelect().ColumnExpr("pg_backend_pid()").Scan(ctx, &blockerPID))
			if visitor {
				_, err = tx.ExecContext(ctx, "SELECT id FROM channel_identities WHERE channel_id = ? FOR UPDATE", f.channelID)
			} else {
				_, err = tx.ExecContext(ctx, "SELECT id FROM users WHERE id = ? FOR UPDATE", f.owner.User.ID)
			}
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() {
				if visitor {
					_, err := f.visitorMessage(ctx, "等待后入站")
					done <- err
				} else {
					_, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "等待后回复"})
					done <- err
				}
			}()
			// 等到请求确实阻塞，再让另一条消息先提交并被阅读。
			for {
				var blocked bool
				require.NoError(t, f.db.NewSelect().ColumnExpr("EXISTS (SELECT 1 FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid)))", blockerPID).Scan(ctx, &blocked))
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
				reply, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "先提交回复"})
				require.NoError(t, err)
				earlierID = reply.ID
			} else {
				inbound, err := f.visitorMessage(ctx, "先提交入站")
				require.NoError(t, err)
				earlierID = inbound.Message.ID
			}
			_, err = conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.member, f.conversationID, earlierID, false)
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			require.NoError(t, <-done)
			row := f.inboxRow(t, f.member, domain.ServiceSessionStatusOpen)
			require.Equal(t, 1, row.UnreadCount, "late commit disappeared from unread")
			var earlier servermodels.Message
			require.NoError(t, f.db.NewSelect().Model(&earlier).Where("msg.id = ?", earlierID).Scan(ctx))
			history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID, After: &conversationaction.MessageCursorPoint{ID: earlier.ID, MessageSeq: earlier.MessageSeq}})
			require.NoError(t, err)
			// 延迟提交的成员回复同时领取周期，领取事件不计入对话消息。
			conversational := arr.Count(history.Messages, func(message conversationaction.ConversationMessage) bool {
				return message.Type != domain.MessageTypeSystem
			})
			require.Equal(t, 1, conversational, "late commit missing from after")
		})
	}
}
