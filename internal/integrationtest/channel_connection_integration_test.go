//go:build server

package integrationtest

import (
	"context"
	"sync"
	"testing"
	"time"

	"uuid"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/channelconnection"
	"github.com/runforyou-ai/luway/internal/actions/channelinbound"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// fakeConnector 是记录连接与发送的长连接测试适配器；rejected 中的机器人编号建立连接时被拒绝。
type fakeConnector struct {
	db       *bun.DB
	sender   channeladapter.SessionSender
	rejected map[string]bool

	mu       sync.Mutex
	sessions map[string]*fakeSession
}

// Plan 把消息作为一个请求项。
func (c *fakeConnector) Plan(body string, attachment bool) []channeladapter.Item {
	return []channeladapter.Item{{Body: body, Attachment: attachment}}
}

// SendTimeout 返回固定超时。
func (c *fakeConnector) SendTimeout(channeladapter.Item) time.Duration { return 5 * time.Second }

// Send 经运行时由本实例持有的连接发送。
func (c *fakeConnector) Send(ctx context.Context, target channeladapter.Target, request channeladapter.Request) channeladapter.Result {
	return c.sender.Send(ctx, target, request)
}

// Revision 以凭据更新时间作为配置版本。
func (c *fakeConnector) Revision(ctx context.Context, target channeladapter.Target) (string, error) {
	var updatedAt time.Time
	err := c.db.NewSelect().TableExpr("wecom_bot_channel_settings").Column("updated_at").Where("channel_id = ?", target.ChannelID).Scan(ctx, &updatedAt)
	return updatedAt.String(), err
}

// Connect 建立测试连接。
func (c *fakeConnector) Connect(_ context.Context, target channeladapter.Target) (channeladapter.Session, error) {
	if c.rejected[target.AccountID] {
		return nil, channeladapter.ErrConnectionRejected
	}
	session := &fakeSession{accountID: target.AccountID, events: make(chan channeladapter.InboundEvent, 8)}
	c.mu.Lock()
	c.sessions[target.ChannelID] = session
	c.mu.Unlock()
	return session, nil
}

// session 返回渠道最近建立的连接；共享测试库中其他测试的渠道也会被连接，按渠道区分。
func (c *fakeConnector) session(channelID string) *fakeSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[channelID]
}

// fakeSession 是一条测试连接，events 中的事件依次交给运行时。
type fakeSession struct {
	accountID string
	events    chan channeladapter.InboundEvent

	mu    sync.Mutex
	sends []string
}

// Revision 返回空版本。
func (s *fakeSession) Revision() string { return "" }

// AccountID 返回连接所属的机器人编号。
func (s *fakeSession) AccountID() string { return s.accountID }

// Close 不做任何事。
func (s *fakeSession) Close() error { return nil }

// Serve 转交测试事件直至 ctx 结束。
func (s *fakeSession) Serve(ctx context.Context, handle func(channeladapter.InboundEvent)) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-s.events:
			handle(event)
		}
	}
}

// Send 记录发送正文。
func (s *fakeSession) Send(_ context.Context, _ channeladapter.Target, request channeladapter.Request) channeladapter.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends = append(s.sends, request.Body)
	return channeladapter.Result{Outcome: channeladapter.OutcomeSent}
}

// waitFor 在超时前反复检查条件。
func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			require.FailNowf(t, "timed out", "timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// connectionHolder 返回持有渠道长连接路由有效租约的实例与连接状态。
func connectionHolder(t *testing.T, db *bun.DB, channelID string) (string, string) {
	t.Helper()
	ctx := context.Background()
	route := &models.TaskRoute{}
	if err := db.NewSelect().Model(route).Where("trt.route_key = ? AND trt.lease_expires_at > now()", channeladapter.ConnectionRoute(channelID)).Scan(ctx); err != nil {
		return "", ""
	}
	connection := &models.ChannelConnection{}
	if err := db.NewSelect().Model(connection).Where("chc.channel_id = ?", channelID).Scan(ctx); err != nil {
		return route.InstanceID, ""
	}
	return route.InstanceID, connection.Status
}

// routedTestTasks 组合真实的任务路由租约与测试任务登记器。
type routedTestTasks struct {
	*servertask.Routes
	*servertest.Tasks
}

// TestChannelConnectionRuntime 验证长连接路由租约只由一个实例持有，入站事件写入可靠任务，外发只经本实例持有的连接发送，
// 持有者停止后其他实例接管，凭据被拒时记录状态。
func TestChannelConnectionRuntime(t *testing.T) {
	ctx := context.Background()
	f := newEmployeeChannelFixture(t)

	inbound := servertest.NewTasks()
	newRuntime := func() (*channelconnection.Runtime, *fakeConnector) {
		tasks := routedTestTasks{Routes: servertask.NewRoutes(f.db, uuid.NewV7().String()), Tasks: inbound}
		adapters := channeladapter.NewRegistry()
		connector := &fakeConnector{db: f.db, rejected: map[string]bool{}, sessions: map[string]*fakeSession{}}
		runtime := channelconnection.New(f.db, adapters, tasks)
		connector.sender = runtime
		adapters.Register(domain.ChannelTypeWeComBot, connector)
		return runtime, connector
	}
	first, firstConnector := newRuntime()
	second, secondConnector := newRuntime()
	target := channeladapter.Target{WorkspaceID: f.identity.Workspace.ID, ChannelID: f.channel.ID, AccountID: f.botID, Recipient: "zhangsan"}

	// 本实例没有连接时外发等待重发。
	result := second.Send(ctx, target, channeladapter.Request{Item: channeladapter.Item{Body: "早"}})
	require.Equal(t, channeladapter.OutcomeRetry, result.Outcome, "send without holder=%+v", result)

	first.Start(ctx)
	waitFor(t, "first holder online", func() bool {
		_, status := connectionHolder(t, f.db, f.channel.ID)
		return status == channelconnection.StatusOnline
	})
	holder, _ := connectionHolder(t, f.db, f.channel.ID)
	second.Start(ctx)
	t.Cleanup(second.Stop)
	time.Sleep(200 * time.Millisecond)
	current, _ := connectionHolder(t, f.db, f.channel.ID)
	require.Equal(t, holder, current, "second instance took the lease")
	require.Nil(t, secondConnector.session(f.channel.ID), "second instance took the lease")

	// 入站事件按渠道与事件编号排重写入可靠任务，并按渠道长连接路由由持有连接的实例处理。
	session := firstConnector.session(f.channel.ID)
	event := channeladapter.InboundEvent{Kind: channeladapter.InboundEventMessage, ID: "evt-1", Sender: "zhangsan", ChatID: "zhangsan", Text: "你好", OccurredAt: time.Now().UTC()}
	session.events <- event
	session.events <- event
	waitFor(t, "inbound task", func() bool {
		count := 0
		for _, task := range inbound.Queued(channelinbound.ReceiveEventActionName, "") {
			if task.Options.IdempotencyKey == "chevt:"+f.channel.ID+":"+f.botID+":evt-1" && task.Options.Route == channeladapter.ConnectionRoute(f.channel.ID) {
				count++
			}
		}
		return count == 1
	})

	// 持有连接的实例经本机连接发送，未持有连接的实例等待重发；平台账号不一致时不发送。
	result = first.Send(ctx, target, channeladapter.Request{Item: channeladapter.Item{Body: "收到"}})
	require.Equal(t, channeladapter.OutcomeSent, result.Outcome, "holder send=%+v", result)
	result = second.Send(ctx, target, channeladapter.Request{Item: channeladapter.Item{Body: "未持有"}})
	require.Equal(t, channeladapter.OutcomeRetry, result.Outcome, "non-holder send=%+v", result)
	changed := target
	changed.AccountID = "other-bot"
	result = first.Send(ctx, changed, channeladapter.Request{Item: channeladapter.Item{Body: "旧机器人"}})
	require.Equal(t, channeladapter.OutcomeFailed, result.Outcome, "changed account send=%+v", result)
	require.Equal(t, "account_changed", result.Code, "changed account send=%+v", result)
	session.mu.Lock()
	sends := append([]string(nil), session.sends...)
	session.mu.Unlock()
	require.Equal(t, []string{"收到"}, sends, "holder sends")

	// 持有者停止后释放租约，其他实例在下次核对时接管。
	first.Stop()
	current, _ = connectionHolder(t, f.db, f.channel.ID)
	require.Empty(t, current, "lease not released")
	third, thirdConnector := newRuntime()
	third.Start(ctx)
	t.Cleanup(third.Stop)
	waitFor(t, "takeover", func() bool {
		current, status := connectionHolder(t, f.db, f.channel.ID)
		return current != "" && current != holder && status == channelconnection.StatusOnline
	})
	require.True(t, thirdConnector.session(f.channel.ID) != nil || secondConnector.session(f.channel.ID) != nil, "no instance connected after takeover")

	// 平台拒绝凭据时记录状态，配置版本变化前不重连。
	rejected, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWeComBot, Name: "凭据错误", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	rejectedBot := "bot-" + uuid.NewV7().String()
	_, err = channelaction.NewSaveWeComBotConnectionAction(f.db).Execute(ctx, f.identity, rejected.ID, channelaction.WeComBotConnectionInput{BotID: rejectedBot, Secret: "wrong"})
	require.NoError(t, err)
	fourth, fourthConnector := newRuntime()
	fourthConnector.rejected[rejectedBot] = true
	second.Stop()
	third.Stop()
	fourth.Start(ctx)
	t.Cleanup(fourth.Stop)
	waitFor(t, "rejected status", func() bool {
		_, status := connectionHolder(t, f.db, rejected.ID)
		return status == channelconnection.StatusRejected
	})
}
