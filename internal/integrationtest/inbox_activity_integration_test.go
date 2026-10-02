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

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
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
			cv := &servermodels.Conversation{ID: uuid.NewV7().String(), OrganizationID: f.owner.Organization.ID, Type: string(kind), Status: "active"}
			if _, err := f.db.NewInsert().Model(cv).Column("id", "organization_id", "type", "status").Exec(ctx); err != nil {
				t.Fatal(err)
			}
			var databaseStart time.Time
			if err := f.db.NewRaw("SELECT clock_timestamp()").Scan(ctx, &databaseStart); err != nil {
				t.Fatal(err)
			}
			key := uuid.NewV7().String()
			message := &servermodels.Message{ID: uuid.NewV7().String(), OrganizationID: cv.OrganizationID, ConversationID: cv.ID, Type: "text", Body: "来源时钟超前", OriginatedAt: databaseStart.Add(24 * time.Hour), IdempotencyKey: &key}
			if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
					return err
				}
				_, inserted, err := chatstate.AppendMessage(ctx, tx, cv, message)
				if err == nil && !inserted {
					return errors.New("first append was not inserted")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var databaseEnd time.Time
			if err := f.db.NewRaw("SELECT clock_timestamp()").Scan(ctx, &databaseEnd); err != nil {
				t.Fatal(err)
			}
			if cv.LastActivityAt == nil || cv.LastActivityAt.Before(databaseStart) || cv.LastActivityAt.After(databaseEnd) {
				t.Fatalf("activity=%v database=[%v,%v]", cv.LastActivityAt, databaseStart, databaseEnd)
			}
			firstActivity := *cv.LastActivityAt
			if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
					return err
				}
				_, inserted, err := chatstate.AppendMessage(ctx, tx, cv, message)
				if err == nil && inserted {
					return errors.New("replay inserted a message")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if !cv.LastActivityAt.Equal(firstActivity) {
				t.Fatal("replay moved activity")
			}
			// 模拟数据库时钟回拨前已经保存的较大活动时间。
			future := databaseStart.Add(48 * time.Hour)
			if _, err := f.db.NewUpdate().Model(cv).Set("last_activity_at = ?", future).WherePK().Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
					return err
				}
				late := &servermodels.Message{ID: uuid.NewV7().String(), OrganizationID: cv.OrganizationID, ConversationID: cv.ID, Type: "system", Body: "晚到消息", OriginatedAt: databaseStart.Add(-24 * time.Hour)}
				_, _, err := chatstate.AppendMessage(ctx, tx, cv, late)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.db.NewSelect().Model(cv).WherePK().Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if cv.LastActivityAt == nil || !cv.LastActivityAt.Equal(future) || cv.LastMessageAt == nil || !cv.LastMessageAt.Equal(databaseStart.Add(-24*time.Hour)) {
				t.Fatalf("late summary=%+v", cv)
			}
			// 用正常时钟再追加后回滚，检查数据库没有留下新的活动时间。
			if _, err := f.db.NewUpdate().Model(cv).Set("last_activity_at = ?", firstActivity).WherePK().Exec(ctx); err != nil {
				t.Fatal(err)
			}
			rollback := errors.New("rollback activity")
			err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
					return err
				}
				if _, _, err := chatstate.AppendMessage(ctx, tx, cv, &servermodels.Message{ID: uuid.NewV7().String(), OrganizationID: cv.OrganizationID, ConversationID: cv.ID, Type: "text", Body: "回滚", OriginatedAt: databaseStart}); err != nil {
					return err
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			if err := f.db.NewSelect().Model(cv).WherePK().Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if cv.LastMessageSeq != 2 || !cv.LastActivityAt.Equal(firstActivity) {
				t.Fatalf("rollback summary=%+v", cv)
			}
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
	login := loginMember(t, f.db, f.owner.Organization.ID, f.member.Account.Email, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
	f.db.AddQueryHook(chatQueryHook{})
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "SELECT" && strings.Contains(event.Query, "AS candidates") && strings.Contains(event.Query, "LIMIT 50")
	})
	var snapshot appservice.Inbox
	done := make(chan error, 1)
	go func() {
		var err error
		snapshot, err = backend.LoadInbox(context.WithValue(ctx, chatQueryGateKey{}, gate), appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Organization.ID}, appservice.LoadInboxInput{Scope: appservice.InboxScopeChat})
		done <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	second := f.send(t, f.owner, "快照后提交", false)
	gate.open()
	if err := waitChatResult(t, ctx, done); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Conversations) != 1 || snapshot.Conversations[0].LastMessageID == nil || *snapshot.Conversations[0].LastMessageID != first.ID || snapshot.Conversations[0].UnreadCount != 1 || snapshot.UnreadCount != 1 || snapshot.AttentionUnreadCount != 1 {
		t.Fatalf("mixed snapshot=%+v rows=%+v", snapshot, snapshot.Conversations)
	}
	current, err := backend.LoadInbox(ctx, appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Organization.ID}, appservice.LoadInboxInput{Scope: appservice.InboxScopeChat})
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Conversations) != 1 || *current.Conversations[0].LastMessageID != second.ID || current.UnreadCount != 2 || current.AttentionUnreadCount != 2 || current.Conversations[0].LastActivityAt == nil {
		t.Fatalf("next snapshot=%+v", current)
	}
}

// TestInboxActivityOrder 验证聊天列表保持微秒顺序、同时间编号倒序及空会话沉底。
func TestInboxActivityOrder(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊"})
	if err != nil {
		t.Fatal(err)
	}
	f.send(t, f.owner, "群聊", false)
	empty, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "空群", MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	third, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "第三个群", MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var base time.Time
	if err := f.db.NewRaw("SELECT date_trunc('milliseconds', clock_timestamp()) - interval '1 hour'").Scan(ctx, &base); err != nil {
		t.Fatal(err)
	}
	ids := []string{direct.Conversation.ID, f.groupID, third.ID}
	for i, id := range ids {
		if _, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", base.Add(time.Duration(2-i)*time.Microsecond)).Set("last_message_at = ?", base.Add(time.Duration(i)*time.Hour)).Where("id = ?", id).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	query := inboxaction.NewLoadInboxQuery(f.db)
	rowsPage, counts, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows := rowsPage.Conversations
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range rows {
		got = append(got, row.ID)
	}
	if !slices.Equal(got, append(slices.Clone(ids), empty.ID)) || counts.Unread != 0 || counts.Attention != 0 {
		t.Fatalf("order=%v counts=%+v", got, counts)
	}
	if rows[2].Group.LastMessageAt == nil || !rows[2].Group.LastMessageAt.Equal(base.Add(2*time.Hour)) || rows[3].LastActivityAt != nil {
		t.Fatalf("preview or empty=%+v", rows)
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", base).Where("id IN (?)", bun.In(ids)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	rowsPage, _, err = query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows = rowsPage.Conversations
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	slices.Reverse(ids)
	got = got[:0]
	for _, row := range rows {
		got = append(got, row.ID)
	}
	if !slices.Equal(got, append(ids, empty.ID)) {
		t.Fatalf("tie order=%v", got)
	}

	// 静音和已读改变个人投影，保留活动时间。
	if _, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.owner, f.groupID, true); err != nil {
		t.Fatal(err)
	}
	var groupMessageID string
	if err := f.db.NewSelect().Table("conversations").Column("last_message_id").Where("id = ?", f.groupID).Scan(ctx, &groupMessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.owner, f.groupID, groupMessageID, false); err != nil {
		t.Fatal(err)
	}
	var groupActivity time.Time
	if err := f.db.NewSelect().Table("conversations").Column("last_activity_at").Where("id = ?", f.groupID).Scan(ctx, &groupActivity); err != nil || !groupActivity.Equal(base) {
		t.Fatalf("settings activity=%v err=%v", groupActivity, err)
	}

	// 核验空群简介更新保留活动状态，改名系统消息更新活动位置。
	if _, err := groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: empty.ID, Title: "空群", Description: "仅资料"}); err != nil {
		t.Fatal(err)
	}
	var activity sql.NullTime
	if err := f.db.NewSelect().Table("conversations").Column("last_activity_at").Where("id = ?", empty.ID).Scan(ctx, &activity); err != nil || activity.Valid {
		t.Fatalf("profile activity=%v err=%v", activity, err)
	}
	if _, err := groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: empty.ID, Title: "新群名", Description: "仅资料"}); err != nil {
		t.Fatal(err)
	}
	rowsPage, _, err = query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows = rowsPage.Conversations
	if err != nil || rows[0].ID != empty.ID || rows[0].LastActivityAt == nil {
		t.Fatalf("system activity=%+v err=%v", rows, err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Table("telegram_channel_settings").Set("bot_id = ?", time.Now().UnixNano()).Set("bot_token = '123:token'").Set("webhook_secret = 'secret'").Where("channel_id = ?", channel.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var before time.Time
	if err := f.db.NewRaw("SELECT clock_timestamp()").Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}
	source := before.Add(-24 * time.Hour)
	receiver := customerchataction.NewReceiveTelegramWebhookAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), domain.FileStorageBackendLocal, newTestTasks(f.db))
	input := customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: 1, Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 1, DisplayName: "晚到客户", Body: "昨天的消息", OriginatedAt: source}}
	if err := receiver.Execute(ctx, channel.ID, input); err != nil {
		t.Fatal(err)
	}
	query := inboxaction.NewLoadInboxQuery(f.db)
	rowsPage, counts, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterUnassigned})
	rows := rowsPage.Conversations
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Service.Channel.Type != domain.ChannelTypeTelegram || rows[0].LastActivityAt == nil || rows[0].LastActivityAt.Before(before) || !rows[0].Service.LastMessageAt.Equal(source) || counts.Unread != 0 || counts.Attention != 0 {
		t.Fatalf("telegram rows=%+v counts=%+v", rows, counts)
	}
	telegram := rows[0]
	if err := receiver.Execute(ctx, channel.ID, input); err != nil {
		t.Fatal(err)
	}
	chatPage, _, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range chatPage.Conversations {
		if row.Service != nil {
			t.Fatalf("customer conversation leaked into chats: %+v", row)
		}
	}
	// 阅读与处理状态更新投影并保留活动位置。
	if _, err := conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.owner, telegram.ID, *telegram.LastMessageID, false); err != nil {
		t.Fatal(err)
	}
	coordinator := agentrunaction.NewExecuteAction(f.db, nil, nil, testAttachmentReader(f.db), nil, nil)
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.owner, telegram.ID); err != nil {
		t.Fatal(err)
	}
	// 队列中关闭的会话由关闭人负责，出现在其负责的已关闭列表。
	closedPage, counts, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: f.owner.OrganizationIdentity.ID, ServiceStatus: domain.ServiceSessionStatusClosed})
	closed := closedPage.Conversations
	if err != nil || len(closed) != 1 || !closed[0].LastActivityAt.Equal(*telegram.LastActivityAt) || closed[0].UnreadCount != 0 || counts.Attention != 0 {
		t.Fatalf("closed=%+v counts=%+v err=%v", closed, counts, err)
	}
	if _, err := servicesessionaction.NewReopenServiceSessionAction(f.db).Execute(ctx, f.owner, telegram.ID); err != nil {
		t.Fatal(err)
	}
	var activity time.Time
	if err := f.db.NewSelect().Table("conversations").Column("last_activity_at").Where("id = ?", telegram.ID).Scan(ctx, &activity); err != nil || !activity.Equal(*telegram.LastActivityAt) {
		t.Fatalf("reopened=%v err=%v", activity, err)
	}
}
