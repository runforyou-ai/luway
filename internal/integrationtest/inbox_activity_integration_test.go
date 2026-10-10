//go:build server

package integrationtest

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestInboxActivityAppend 验证各类消息活动取数据库时钟、单调推进，并与消息一起回滚。
func TestInboxActivityAppend(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	for _, kind := range []domain.ConversationType{domain.ConversationTypeDirect, domain.ConversationTypeAgent, domain.ConversationTypeGroup, domain.ConversationTypeChannel} {
		t.Run(string(kind), func(t *testing.T) {
			cv := &servermodels.Conversation{ID: uuid.NewV7().String(), WorkspaceID: f.owner.Workspace.ID, Type: string(kind), Status: "active"}
			_, err := f.db.NewInsert().Model(cv).Column("id", "workspace_id", "type", "status").Exec(ctx)
			require.NoError(t, err)
			var databaseStart time.Time
			require.NoError(t, f.db.NewRaw("SELECT clock_timestamp()").Scan(ctx, &databaseStart))
			key := uuid.NewV7().String()
			message := &servermodels.Message{ID: uuid.NewV7().String(), WorkspaceID: cv.WorkspaceID, ConversationID: cv.ID, Type: "text", Body: "来源时钟超前", OriginatedAt: databaseStart.Add(24 * time.Hour), IdempotencyKey: &key}
			require.NoError(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
					return err
				}
				_, inserted, err := chatstate.AppendMessage(ctx, tx, testEnqueuer, cv, message)
				if err == nil && !inserted {
					return errors.New("first append was not inserted")
				}
				return err
			}))
			var databaseEnd time.Time
			require.NoError(t, f.db.NewRaw("SELECT clock_timestamp()").Scan(ctx, &databaseEnd))
			require.NotNil(t, cv.LastActivityAt)
			require.False(t, cv.LastActivityAt.Before(databaseStart), "activity=%v database=[%v,%v]", cv.LastActivityAt, databaseStart, databaseEnd)
			require.False(t, cv.LastActivityAt.After(databaseEnd), "activity=%v database=[%v,%v]", cv.LastActivityAt, databaseStart, databaseEnd)
			firstActivity := *cv.LastActivityAt
			require.NoError(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
					return err
				}
				_, inserted, err := chatstate.AppendMessage(ctx, tx, testEnqueuer, cv, message)
				if err == nil && inserted {
					return errors.New("replay inserted a message")
				}
				return err
			}))
			require.True(t, cv.LastActivityAt.Equal(firstActivity), "replay moved activity")
			// 模拟数据库时钟回拨前已经保存的较大活动时间。
			future := databaseStart.Add(48 * time.Hour)
			_, err = f.db.NewUpdate().Model(cv).Set("last_activity_at = ?", future).WherePK().Exec(ctx)
			require.NoError(t, err)
			require.NoError(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
					return err
				}
				late := &servermodels.Message{ID: uuid.NewV7().String(), WorkspaceID: cv.WorkspaceID, ConversationID: cv.ID, Type: "system", Body: "晚到消息", OriginatedAt: databaseStart.Add(-24 * time.Hour)}
				_, _, err := chatstate.AppendMessage(ctx, tx, testEnqueuer, cv, late)
				return err
			}))
			require.NoError(t, f.db.NewSelect().Model(cv).WherePK().Scan(ctx))
			require.NotNil(t, cv.LastActivityAt)
			require.True(t, cv.LastActivityAt.Equal(future), "late summary=%+v", cv)
			require.NotNil(t, cv.LastMessageAt)
			require.True(t, cv.LastMessageAt.Equal(databaseStart.Add(-24*time.Hour)), "late summary=%+v", cv)
			// 用正常时钟再追加后回滚，检查数据库没有留下新的活动时间。
			_, err = f.db.NewUpdate().Model(cv).Set("last_activity_at = ?", firstActivity).WherePK().Exec(ctx)
			require.NoError(t, err)
			rollback := errors.New("rollback activity")
			err = realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
					return err
				}
				if _, _, err := chatstate.AppendMessage(ctx, tx, testEnqueuer, cv, &servermodels.Message{ID: uuid.NewV7().String(), WorkspaceID: cv.WorkspaceID, ConversationID: cv.ID, Type: "text", Body: "回滚", OriginatedAt: databaseStart}); err != nil {
					return err
				}
				return rollback
			})
			require.ErrorIs(t, err, rollback)
			require.NoError(t, f.db.NewSelect().Model(cv).WherePK().Scan(ctx))
			require.Equal(t, int64(2), cv.LastMessageSeq)
			require.True(t, cv.LastActivityAt.Equal(firstActivity), "rollback summary=%+v", cv)
		})
	}
}

// TestInboxSnapshot 验证响应未读总数使用列表读取时的快照。
func TestInboxSnapshot(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first := f.send(t, f.owner, "快照前", false)
	login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.member.Account.Email, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	f.db.AddQueryHook(chatQueryHook{})
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "SELECT" && strings.Contains(event.Query, "AS candidates") && strings.Contains(event.Query, "LIMIT 50")
	})
	var snapshot appservice.Inbox
	done := make(chan error, 1)
	go func() {
		var err error
		snapshot, err = backend.LoadInbox(context.WithValue(ctx, chatQueryGateKey{}, gate), appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID}, appservice.LoadInboxInput{Scope: domain.InboxScopeChat})
		done <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	second := f.send(t, f.owner, "快照后提交", false)
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, done))
	require.Len(t, snapshot.Conversations, 1)
	require.NotNil(t, snapshot.Conversations[0].LastMessageID)
	require.Equal(t, first.ID, *snapshot.Conversations[0].LastMessageID)
	require.Equal(t, 1, snapshot.Conversations[0].UnreadCount)
	require.Equal(t, 1, snapshot.UnreadCount)
	require.Equal(t, 1, snapshot.AttentionUnreadCount)
	current, err := backend.LoadInbox(ctx, appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID}, appservice.LoadInboxInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Len(t, current.Conversations, 1)
	require.Equal(t, second.ID, *current.Conversations[0].LastMessageID)
	require.Equal(t, 2, current.UnreadCount)
	require.Equal(t, 2, current.AttentionUnreadCount)
	require.NotNil(t, current.Conversations[0].LastActivityAt)
}

// TestInboxActivityOrder 验证聊天列表保持微秒顺序、同时间编号倒序及空会话沉底。
func TestInboxActivityOrder(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊"})
	require.NoError(t, err)
	f.send(t, f.owner, "群聊", false)
	empty, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "空群", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	third, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "第三个群", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	var base time.Time
	require.NoError(t, f.db.NewRaw("SELECT date_trunc('milliseconds', clock_timestamp()) - interval '1 hour'").Scan(ctx, &base))
	ids := []string{direct.Conversation.ID, f.groupID, third.ID}
	for i, id := range ids {
		_, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", base.Add(time.Duration(2-i)*time.Microsecond)).Set("last_message_at = ?", base.Add(time.Duration(i)*time.Hour)).Where("id = ?", id).Exec(ctx)
		require.NoError(t, err)
	}
	query := inboxaction.NewLoadInboxQuery(f.db)
	rowsPage, counts, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows := rowsPage.Conversations
	require.NoError(t, err)
	var got []string
	for _, row := range rows {
		got = append(got, row.ID)
	}
	require.Equal(t, append(slices.Clone(ids), empty.ID), got)
	require.Zero(t, counts.Unread)
	require.Zero(t, counts.Attention)
	require.NotNil(t, rows[2].Group.LastMessageAt)
	require.True(t, rows[2].Group.LastMessageAt.Equal(base.Add(2*time.Hour)), "preview=%+v", rows[2])
	require.Nil(t, rows[3].LastActivityAt)
	_, err = f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", base).Where("id IN (?)", bun.List(ids)).Exec(ctx)
	require.NoError(t, err)
	rowsPage, _, err = query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows = rowsPage.Conversations
	require.NoError(t, err)
	slices.Sort(ids)
	slices.Reverse(ids)
	got = got[:0]
	for _, row := range rows {
		got = append(got, row.ID)
	}
	require.Equal(t, append(ids, empty.ID), got, "tie order")

	// 静音和已读改变个人投影，保留活动时间。
	_, err = conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.owner, f.groupID, true)
	require.NoError(t, err)
	var groupMessageID string
	require.NoError(t, f.db.NewSelect().Table("conversations").Column("last_message_id").Where("id = ?", f.groupID).Scan(ctx, &groupMessageID))
	_, err = conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.owner, f.groupID, groupMessageID, false)
	require.NoError(t, err)
	var groupActivity time.Time
	require.NoError(t, f.db.NewSelect().Table("conversations").Column("last_activity_at").Where("id = ?", f.groupID).Scan(ctx, &groupActivity))
	require.True(t, groupActivity.Equal(base), "settings activity=%v", groupActivity)

	// 核验空群简介更新保留活动状态，改名系统消息更新活动位置。
	_, err = groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: empty.ID, Title: "空群", Description: "仅资料"})
	require.NoError(t, err)
	var activity sql.NullTime
	require.NoError(t, f.db.NewSelect().Table("conversations").Column("last_activity_at").Where("id = ?", empty.ID).Scan(ctx, &activity))
	require.False(t, activity.Valid, "profile activity=%v", activity)
	_, err = groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: empty.ID, Title: "新群名", Description: "仅资料"})
	require.NoError(t, err)
	rowsPage, _, err = query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows = rowsPage.Conversations
	require.NoError(t, err)
	require.Equal(t, empty.ID, rows[0].ID)
	require.NotNil(t, rows[0].LastActivityAt)
}

// TestInboxTelegramActivity 验证晚到的 Telegram 消息更新活动排序，保留来源时间和原客户范围。
func TestInboxTelegramActivity(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	channel, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeTelegram, Name: "活动时间验证", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	require.NoError(t, connectTestTelegramBot(ctx, f.db, channel.ID, time.Now().UnixNano(), "123:token"))
	var before time.Time
	require.NoError(t, f.db.NewRaw("SELECT clock_timestamp()").Scan(ctx, &before))
	source := before.Add(-24 * time.Hour)
	receiver := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	input := telegramUpdate{Secret: "secret", UpdateID: 1, Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 1, DisplayName: "晚到客户", Body: "昨天的消息", OriginatedAt: source}}
	require.NoError(t, receiver.Execute(ctx, channel.ID, input))
	query := inboxaction.NewLoadInboxQuery(f.db)
	rowsPage, counts, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterUnassigned})
	rows := rowsPage.Conversations
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, domain.ChannelTypeTelegram, rows[0].Service.Channel.Type)
	require.NotNil(t, rows[0].LastActivityAt)
	require.False(t, rows[0].LastActivityAt.Before(before), "telegram activity=%v", rows[0].LastActivityAt)
	require.True(t, rows[0].Service.LastMessageAt.Equal(source), "telegram last message=%v", rows[0].Service.LastMessageAt)
	require.Zero(t, counts.Unread)
	require.Zero(t, counts.Attention)
	telegram := rows[0]
	require.NoError(t, receiver.Execute(ctx, channel.ID, input))
	chatPage, _, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	for _, row := range chatPage.Conversations {
		require.Nil(t, row.Service, "customer conversation leaked into chats: %+v", row)
	}
	// 阅读与处理状态更新投影并保留活动位置。
	_, err = conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.owner, telegram.ID, *telegram.LastMessageID, false)
	require.NoError(t, err)
	coordinator := newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.owner, telegram.ID)
	require.NoError(t, err)
	// 队列中关闭的会话由关闭人负责，出现在其负责的已关闭列表。
	closedPage, counts, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: f.owner.WorkspaceIdentity.ID, ServiceStatus: domain.ServiceSessionStatusClosed})
	closed := closedPage.Conversations
	require.NoError(t, err)
	require.Len(t, closed, 1)
	require.True(t, closed[0].LastActivityAt.Equal(*telegram.LastActivityAt), "closed activity=%v", closed[0].LastActivityAt)
	require.Zero(t, closed[0].UnreadCount)
	require.Zero(t, counts.Attention)
	_, err = servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer).Execute(ctx, f.owner, telegram.ID)
	require.NoError(t, err)
	var activity time.Time
	require.NoError(t, f.db.NewSelect().Table("conversations").Column("last_activity_at").Where("id = ?", telegram.ID).Scan(ctx, &activity))
	require.True(t, activity.Equal(*telegram.LastActivityAt), "reopened=%v", activity)
}
