//go:build server

package integrationtest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/einorun/stream"
	"github.com/runforyou-ai/jetcast/client"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/runforyou-ai/luway/internal/servertest"
	"github.com/runforyou-ai/support/random"
	"github.com/stretchr/testify/require"
)

// runReplica 创建共享 NATS 的独立执行实例，发布故障由调用方注入。
func runReplica(t *testing.T, h *realtimeGatewayHarness, fail *atomic.Bool) (*realtime.Publisher, *nats.Conn) {
	t.Helper()
	options := h.broker.Conn.Opts
	options.Name = "run-replica"
	nc, err := options.Connect()
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	service, err := members.New(&broker.Connection{Conn: nc, Signer: h.broker.Signer, Admin: h.broker.Admin}, h.backend, h.db, "app_realtime", 1)
	require.NoError(t, err)
	require.NoError(t, service.Start(t.Context()))
	t.Cleanup(func() { _ = service.Stop() })
	publisher := realtime.NewPublisher(servertest.StartBus(t, h.db, h.channel, uuid.NewV7().String()))
	publisher.SetRunTransport(nc, "app_realtime", func(ctx context.Context, workspaceID string, event realtime.RunStreamEvent) error {
		if fail != nil && fail.Load() {
			return errors.New("injected publication failure")
		}
		return service.PublishRun(ctx, workspaceID, event)
	})
	require.NoError(t, publisher.Start())
	t.Cleanup(func() { _ = publisher.Stop() })
	return publisher, nc
}

// subscribeRun 在已有账号连接安装运行订阅并等待精确频道就绪。
func subscribeRun(t *testing.T, h *realtimeGatewayHarness, echo *client.Client, runID string) (*client.Subscription, *realtimeTestClient) {
	t.Helper()
	subscription := echo.Private(realtime.RunChannel(h.workspaceID, runID))
	events := &realtimeTestClient{t: t, echo: echo, frames: make(chan protocol.Frame, 128)}
	subscription.ListenAll(func(event client.Event) {
		frame, err := protocol.Decode(event.Data)
		if err != nil {
			t.Errorf("decode run event: %v", err)
			return
		}
		select {
		case events.frames <- frame:
		case <-t.Context().Done():
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, subscription.Ready(ctx))
	t.Cleanup(subscription.Leave)
	return subscription, events
}

// runState 经正式 Backend 读取当前成员可见的运行快照。
func runState(t *testing.T, h *realtimeGatewayHarness, token, runID string) appservice.AgentRunStreamState {
	t.Helper()
	result, err := h.backend.GetAgentRunStreamState(t.Context(), appservice.RequestMeta{Token: token, WorkspaceID: h.workspaceID}, runID)
	require.NoError(t, err)
	return result
}

// appendRunText 发布一条连续业务序号的候选正文增量。
func appendRunText(source *realtime.RunStreamSource, runID, streamID string, attempt int, sequence int64, text string) {
	source.Publish(stream.Delta{Stream: streamID, Base: sequence - 1, Sequence: sequence,
		Operations: []stream.Operation{{Kind: stream.OpAppendCandidate, Text: text}}})
}

// TestJetcastRunIsolationAndRevocation 验证精确频道、工作区隔离、失权与同账号成员通知共存。
func TestJetcastRunIsolationAndRevocation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	runID := insertAgentRun(t, f.db, h.workspaceID, f.groupID)
	member, echo := h.connectMemberChannels(t, f.memberToken, true)
	subscription, events := subscribeRun(t, h, echo, runID)
	source := h.publisher.OpenRunStream(t.Context(), h.workspaceID, 1, realtime.RunSnapshot{RunID: runID, Attempt: 1, Snapshot: stream.Snapshot{Stream: "first"}})
	t.Cleanup(source.End)
	executeRunOn(t, f.db, h.workspaceID, runID, h.publisher.Bus().ID(), 1)
	require.NotNil(t, runState(t, h, f.memberToken, runID).Snapshot)
	appendRunText(source, runID, "first", 1, 1, "visible")
	events.expect(protocol.RunStreamDelta{RunID: runID, StreamID: "first", Attempt: 1, Sequence: 1, Operations: []protocol.RunStreamOperation{{Kind: protocol.RunStreamAppendCandidate, Text: "visible"}}})
	for _, name := range []string{realtime.RunChannel(uuid.NewV7().String(), runID), realtime.RunChannel(h.workspaceID, uuid.NewV7().String())} {
		denied := echo.Private(name)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		require.Error(t, denied.Ready(ctx))
		cancel()
		denied.Leave()
	}
	notice := realtime.UserIdentityProfileChanged(h.workspaceID, f.member.User.ID, 3)
	require.NoError(t, h.members.Publish(t.Context(), notice))
	member.expect(members.NotificationFrame(notice))
	// 忽略 SDK 控制事件的原始连接仍由服务端强制关闭。
	raw, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+"/nats", nats.Name("ABCDEFGHIJKLMNOPQRSTUV"), nats.Token(f.memberToken), nats.NoReconnect())
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	_, err = h.backend.RemoveGroupConversationMember(t.Context(), appservice.RequestMeta{Token: f.ownerToken, WorkspaceID: h.workspaceID}, f.groupID, appservice.GroupConversationMemberInput{MemberIdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	require.Eventually(t, raw.IsClosed, 5*time.Second, 10*time.Millisecond)
	_, err = h.backend.GetAgentRunStreamState(t.Context(), appservice.RequestMeta{Token: f.memberToken, WorkspaceID: h.workspaceID}, runID)
	require.Error(t, err)
	subscription.Leave()
	require.Eventually(t, func() bool { return h.members.Server.Stats().Relays == 0 }, 5*time.Second, 10*time.Millisecond)
	// 重新认证后，其他成员频道仍可用而该运行被拒绝。
	remaining, current := h.connectMemberChannels(t, f.memberToken, true)
	denied := current.Private(realtime.RunChannel(h.workspaceID, runID))
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	require.Error(t, denied.Ready(ctx))
	require.NoError(t, h.members.Publish(t.Context(), notice))
	remaining.expect(members.NotificationFrame(notice))
}

// TestJetcastRunSnapshotsAndAttempts 验证同实例和跨实例大快照、快照窗口增量及新尝试接管。
func TestJetcastRunSnapshotsAndAttempts(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	runID := insertAgentRun(t, f.db, h.workspaceID, f.groupID)
	_, echo := h.connectMemberChannels(t, f.memberToken, true)
	subscription, events := subscribeRun(t, h, echo, runID)
	replica, _ := runReplica(t, h, nil)
	text := base64.StdEncoding.EncodeToString(random.Bytes(2 << 20))
	for i, publisher := range []*realtime.Publisher{h.publisher, replica} {
		attempt := i + 1
		streamID := uuid.NewV7().String()
		executeRunOn(t, f.db, h.workspaceID, runID, publisher.Bus().ID(), attempt)
		source := publisher.OpenRunStream(t.Context(), h.workspaceID, uint64(attempt), realtime.RunSnapshot{RunID: runID, Attempt: attempt, Snapshot: stream.Snapshot{Stream: streamID, Candidate: text,
			Blocks: []stream.Block{{ID: "block", Kind: stream.KindThinking, Text: text}}}})
		t.Cleanup(source.End)
		// ready 已完成；HTTP 读取期间产生的事件可在快照后按业务序号去重。
		appendRunText(source, runID, streamID, attempt, 1, "窗口内")
		snapshot := runState(t, h, f.memberToken, runID)
		require.Equal(t, attempt, snapshot.Attempt)
		require.NotNil(t, snapshot.Snapshot)
		require.Equal(t, text+"窗口内", snapshot.Snapshot.CandidateContent)
		require.Equal(t, text, snapshot.Snapshot.Blocks[0].Text)
		event := events.next().(protocol.RunStreamDelta)
		require.Equal(t, int64(1), event.Sequence)
		require.Equal(t, "1", snapshot.Snapshot.Sequence)
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.url+"/api/agent-runs/"+runID+"/stream-state", nil)
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer "+f.memberToken)
		request.Header.Set(appservice.WorkspaceHeader, h.workspaceID)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		var overHTTP appservice.AgentRunStreamState
		require.NoError(t, json.NewDecoder(response.Body).Decode(&overHTTP))
		require.NoError(t, response.Body.Close())
		require.NotNil(t, overHTTP.Snapshot)
		require.Equal(t, "1", overHTTP.Snapshot.Sequence)
		require.Equal(t, text, overHTTP.Snapshot.Blocks[0].Text)
		// 超过消息预算的增量转为明确的失效事件，再读取完整快照。
		appendRunText(source, runID, streamID, attempt, 2, text)
		require.IsType(t, protocol.RunStreamInvalidated{}, events.next())
		require.Equal(t, text+"窗口内"+text, runState(t, h, f.memberToken, runID).Snapshot.CandidateContent)
	}
	subscription.Leave()
	require.Eventually(t, func() bool { return h.members.Server.Stats().Relays == 0 }, 5*time.Second, 10*time.Millisecond)
	before := h.broker.Conn.NumSubscriptions()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := h.publisher.ReadRunSnapshot(ctx, replica.Bus().ID(), runID, 2)
	require.Error(t, err)
	require.Eventually(t, func() bool { return h.broker.Conn.NumSubscriptions() == before }, time.Second, 10*time.Millisecond)
}

// TestJetcastRunPublicationFailureAndCrash 验证未发布增量、末次事件丢失与执行源退出通过业务快照和持久状态收敛。
func TestJetcastRunPublicationFailureAndCrash(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	var fail atomic.Bool
	replica, nc := runReplica(t, h, &fail)
	runID := insertAgentRun(t, f.db, h.workspaceID, f.groupID)
	executeRunOn(t, f.db, h.workspaceID, runID, replica.Bus().ID(), 1)
	source := replica.OpenRunStream(t.Context(), h.workspaceID, 1, realtime.RunSnapshot{RunID: runID, Attempt: 1, Snapshot: stream.Snapshot{Stream: "first"}})
	t.Cleanup(source.End)
	_, echo := h.connectMemberChannels(t, f.memberToken, true)
	_, events := subscribeRun(t, h, echo, runID)
	fail.Store(true)
	appendRunText(source, runID, "first", 1, 1, "未发布内容")
	require.Eventually(t, func() bool { return runState(t, h, f.memberToken, runID).Snapshot.Sequence == "1" }, time.Second, 10*time.Millisecond)
	require.Equal(t, "未发布内容", runState(t, h, f.memberToken, runID).Snapshot.CandidateContent)
	select {
	case frame := <-events.frames:
		t.Fatalf("unexpected failed publication: %#v", frame)
	case <-time.After(100 * time.Millisecond):
	}
	fail.Store(false)
	require.IsType(t, protocol.RunStreamInvalidated{}, events.next())
	// 源连接直接退出且没有 end，持久运行仍为 running，快照暂不可用。
	nc.Close()
	unavailable := runState(t, h, f.memberToken, runID)
	require.Equal(t, domain.AgentRunStatusRunning, unavailable.Status)
	require.Nil(t, unavailable.Snapshot)
	// 新尝试由另一实例接手，业务序号从零开始。
	executeRunOn(t, f.db, h.workspaceID, runID, h.publisher.Bus().ID(), 2)
	replacement := h.publisher.OpenRunStream(t.Context(), h.workspaceID, 2, realtime.RunSnapshot{RunID: runID, Attempt: 2, Snapshot: stream.Snapshot{Stream: "second"}})
	t.Cleanup(replacement.End)
	state := runState(t, h, f.memberToken, runID)
	require.Equal(t, 2, state.Attempt)
	require.Equal(t, "second", state.Snapshot.StreamID)
	require.Equal(t, "0", state.Snapshot.Sequence)
	// 持久终态独立于任何结束事件。
	_, err := f.db.NewUpdate().Table("agent_runs").Set("status = ?", domain.AgentRunStatusFailed).Where("id = ?", runID).Exec(t.Context())
	require.NoError(t, err)
	state = runState(t, h, f.memberToken, runID)
	require.Equal(t, domain.AgentRunStatusFailed, state.Status)
	require.Nil(t, state.Snapshot)
	fail.Store(true)
	source.End()
}

// TestJetcastRunReconnectRecovery 验证运行中继在断线后补发历史，历史被清空时通过 Backend 快照恢复。
func TestJetcastRunReconnectRecovery(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	runID := insertAgentRun(t, f.db, h.workspaceID, f.groupID)
	executeRunOn(t, f.db, h.workspaceID, runID, h.publisher.Bus().ID(), 1)
	source := h.publisher.OpenRunStream(t.Context(), h.workspaceID, 1, realtime.RunSnapshot{RunID: runID, Attempt: 1, Snapshot: stream.Snapshot{Stream: "recovery"}})
	t.Cleanup(source.End)
	var gate atomic.Pointer[chan struct{}]
	echo, err := client.Connect(t.Context(), client.Options{Servers: []string{"ws" + strings.TrimPrefix(h.url, "http") + "/nats"}, Prefix: "app_realtime", GetToken: func(ctx context.Context) (string, error) {
		if paused := gate.Load(); paused != nil {
			select {
			case <-*paused:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return f.memberToken, nil
	}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = echo.Close() })
	subscription, events := subscribeRun(t, h, echo, runID)
	states := make(chan client.State, 32)
	subscription.OnState(func(state client.State) {
		select {
		case states <- state:
		case <-t.Context().Done():
		}
	})
	registry, err := h.broker.JS.KeyValue(t.Context(), "app_realtime_events_CONN")
	require.NoError(t, err)
	var sequence int64
	for _, purge := range []bool{false, true} {
		for len(states) > 0 {
			<-states
		}
		pause := make(chan struct{})
		gate.Store(&pause)
		record, err := registry.Get(t.Context(), "s."+echo.SocketID())
		require.NoError(t, err)
		var connection struct {
			Server string `json:"server"`
			CID    uint64 `json:"cid"`
		}
		require.NoError(t, json.Unmarshal(record.Value(), &connection))
		require.NoError(t, h.broker.Admin.Kick(t.Context(), connection.Server, connection.CID))
		require.Eventually(t, func() bool { return echo.Status() == client.StatusReconnecting }, 3*time.Second, 10*time.Millisecond)
		sequence++
		appendRunText(source, runID, "recovery", 1, sequence, "恢复")
		stream, err := h.broker.JS.Stream(t.Context(), "app_realtime_events")
		require.NoError(t, err)
		subject := "app_realtime.in.prv." + realtime.RunChannel(h.workspaceID, runID)
		require.Eventually(t, func() bool {
			msg, err := stream.GetLastMsgForSubject(t.Context(), subject)
			if err != nil {
				return false
			}
			frame, err := protocol.Decode(msg.Data)
			delta, ok := frame.(protocol.RunStreamDelta)
			return err == nil && ok && delta.Sequence == sequence
		}, 3*time.Second, 10*time.Millisecond)
		if purge {
			require.NoError(t, stream.Purge(t.Context()))
		}
		close(pause)
		gate.Store(nil)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		var recovered client.State
		for recovered.State != client.StateSubscribed {
			select {
			case recovered = <-states:
			case <-ctx.Done():
				t.Fatal("run channel not recovered")
			}
		}
		cancel()
		require.Equal(t, !purge, recovered.Recovered, recovered.Reason)
		if !purge {
			require.IsType(t, protocol.RunStreamDelta{}, events.next())
		}
		require.Equal(t, strings.Repeat("恢复", int(sequence)), runState(t, h, f.memberToken, runID).Snapshot.CandidateContent)
	}
}

// TestJetcastRunActualModelAndCancellation 验证真实 Agent Action、免费本机模型流与取消收尾。
func TestJetcastRunActualModelAndCancellation(t *testing.T) {
	t.Parallel()
	db, owner, providerID, modelID := newAIWorkspace(t)
	token := loginToken(t, db, owner.Workspace.ID, owner.Account.Email)
	h := startRealtimeGateway(t, navigationFixture{db: db, owner: owner})
	entered := make(chan struct{})
	mock := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := writer.(http.Flusher)
		if !ok {
			return
		}
		for _, delta := range []map[string]string{{"role": "assistant"}, {"reasoning_content": "检查当前输入。"}, {"content": "这是一段本机模拟模型的真实流式回复。"}} {
			data, _ := json.Marshal(map[string]any{"id": "local-run", "object": "chat.completion.chunk", "model": "chat-model", "choices": []any{map[string]any{"index": 0, "delta": delta}}})
			_, _ = fmt.Fprintf(writer, "data: %s\n\n", data)
			flusher.Flush()
		}
		close(entered)
		<-request.Context().Done()
	}))
	t.Cleanup(mock.Close)
	_, err := db.NewUpdate().Table("ai_providers").Set("api_url = ?", mock.URL+"/v1").Where("id = ?", providerID).Exec(t.Context())
	require.NoError(t, err)
	agent, err := agentaction.NewCreateAgentAction(db).Execute(t.Context(), owner, agentaction.CreateInput{DisplayName: "运行流测试", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "直接回复用户。"}}})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	_, run := createAgentLockChat(t, t.Context(), db, owner, agent.IdentityID, tasks)
	_, echo := h.connectMemberChannels(t, token, true)
	_, events := subscribeRun(t, h, echo, run.ID)
	runtime, err := agentruntime.New()
	require.NoError(t, err)
	executor := newTestAgentRun(db, tasks, runtime, modelcall.New(db, modelcall.DefaultUpstreams(), nil), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("local model was not called")
	}
	// 直接 Action 测试的执行尝试为零，登记其所在实例供正式快照查询定位。
	executeRunOn(t, db, h.workspaceID, run.ID, h.publisher.Bus().ID(), 0)
	require.Eventually(t, func() bool {
		state := runState(t, h, token, run.ID)
		return state.Snapshot != nil && (len(state.Snapshot.Blocks) > 0 || state.Snapshot.CandidateContent != "")
	}, 5*time.Second, 20*time.Millisecond)
	require.IsType(t, protocol.RunStreamDelta{}, events.next())
	_, err = executor.StopAgentReply(t.Context(), owner, run.ConversationID, run.ID)
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("cancelled run did not settle")
	}
	state := runState(t, h, token, run.ID)
	require.Equal(t, domain.AgentRunStatusCancelled, state.Status)
	require.Nil(t, state.Snapshot)
	process, err := h.backend.GetAgentRunProcess(t.Context(), appservice.RequestMeta{Token: token, WorkspaceID: h.workspaceID}, run.ID)
	require.NoError(t, err)
	require.NotEmpty(t, process.Blocks)
}

// TestJetcastRunSnapshotIntegrityAndDeadline 验证业务授权后的跨实例读取拒绝损坏分块并释放取消请求。
func TestJetcastRunSnapshotIntegrityAndDeadline(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	runID := insertAgentRun(t, f.db, h.workspaceID, f.groupID)
	instanceID := uuid.NewV7().String()
	executeRunOn(t, f.db, h.workspaceID, runID, instanceID, 1)
	mode := atomic.Int32{}
	sub, err := h.broker.Conn.Subscribe("app_realtime_runs.snapshot."+instanceID, func(msg *nats.Msg) {
		switch mode.Load() {
		case 0:
			// 声明的总长度超出协议上限。
			_ = msg.Respond([]byte(`{"Size":67108865,"Count":513}`))
		case 1:
			// 一个完整片段的摘要与内容不符。
			_ = msg.Respond([]byte(`{"Size":2,"Count":1,"Digest":"invalid","Data":"e30="}`))
		case 2:
			// 首个片段必须从零开始。
			_ = msg.Respond([]byte(`{"Index":1,"Size":2,"Count":1,"Data":"e30="}`))
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	require.NoError(t, h.broker.Conn.Flush())
	for i := int32(0); i < 3; i++ {
		mode.Store(i)
		_, err := h.backend.GetAgentRunStreamState(t.Context(), appservice.RequestMeta{Token: f.memberToken, WorkspaceID: h.workspaceID}, runID)
		require.Error(t, err)
	}
	mode.Store(3)
	before := h.broker.Conn.NumSubscriptions()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	_, err = h.backend.GetAgentRunStreamState(ctx, appservice.RequestMeta{Token: f.memberToken, WorkspaceID: h.workspaceID}, runID)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Eventually(t, func() bool { return before == h.broker.Conn.NumSubscriptions() }, time.Second, 10*time.Millisecond)
}

// TestJetcastRunBusinessGapAndSameNodeTakeover 验证业务序号缺口恢复与同实例新尝试替换。
func TestJetcastRunBusinessGapAndSameNodeTakeover(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	runID := insertAgentRun(t, f.db, h.workspaceID, f.groupID)
	executeRunOn(t, f.db, h.workspaceID, runID, h.publisher.Bus().ID(), 1)
	first := h.publisher.OpenRunStream(t.Context(), h.workspaceID, 1, realtime.RunSnapshot{RunID: runID, Attempt: 1, Snapshot: stream.Snapshot{Stream: "first"}})
	t.Cleanup(first.End)
	_, echo := h.connectMemberChannels(t, f.memberToken, true)
	_, events := subscribeRun(t, h, echo, runID)
	baseline := runState(t, h, f.memberToken, runID)
	require.Equal(t, "0", baseline.Snapshot.Sequence)
	appendRunText(first, runID, "first", 1, 1, "one")
	_ = events.next()
	appendRunText(first, runID, "first", 1, 2, "two")
	delta := events.next().(protocol.RunStreamDelta)
	current := stream.Snapshot{Stream: "first"}
	_, err := current.Apply(stream.Delta{Stream: delta.StreamID, Base: delta.BaseSequence, Sequence: delta.Sequence})
	require.ErrorIs(t, err, stream.ErrGap)
	require.Equal(t, "onetwo", runState(t, h, f.memberToken, runID).Snapshot.CandidateContent)
	executeRunOn(t, f.db, h.workspaceID, runID, h.publisher.Bus().ID(), 2)
	second := h.publisher.OpenRunStream(t.Context(), h.workspaceID, 2, realtime.RunSnapshot{RunID: runID, Attempt: 2, Snapshot: stream.Snapshot{Stream: "second", Candidate: "replacement"}})
	t.Cleanup(second.End)
	appendRunText(first, runID, "first", 1, 3, "obsolete")
	latest := runState(t, h, f.memberToken, runID)
	require.Equal(t, 2, latest.Attempt)
	require.Equal(t, "0", latest.Snapshot.Sequence)
	require.Equal(t, "replacement", latest.Snapshot.CandidateContent)
	_, err = h.publisher.ReadRunSnapshot(t.Context(), h.publisher.Bus().ID(), runID, 1)
	require.ErrorIs(t, err, realtime.ErrRunSourceUnavailable)
}

// TestJetcastRunAdmissionOrder 验证运行流登记按本实例准入顺序取代：挂起恢复的新任务即使尝试序号更小也取代旧登记，晚于新登记完成的旧登记不取代当前执行源。
func TestJetcastRunAdmissionOrder(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	runID := insertAgentRun(t, f.db, h.workspaceID, f.groupID)
	executeRunOn(t, f.db, h.workspaceID, runID, h.publisher.Bus().ID(), 2)
	retried := h.publisher.OpenRunStream(t.Context(), h.workspaceID, 1, realtime.RunSnapshot{RunID: runID, Attempt: 2, Snapshot: stream.Snapshot{Stream: "retried"}})
	t.Cleanup(retried.End)
	// 挂起后恢复的新任务从尝试 1 开始，准入序号更大。
	executeRunOn(t, f.db, h.workspaceID, runID, h.publisher.Bus().ID(), 1)
	resumed := h.publisher.OpenRunStream(t.Context(), h.workspaceID, 2, realtime.RunSnapshot{RunID: runID, Attempt: 1, Snapshot: stream.Snapshot{Stream: "resumed", Candidate: "resumed"}})
	t.Cleanup(resumed.End)
	require.Equal(t, "resumed", runState(t, h, f.memberToken, runID).Snapshot.CandidateContent)
	// 准入更早、登记更晚的执行源不取代当前登记。
	stale := h.publisher.OpenRunStream(t.Context(), h.workspaceID, 1, realtime.RunSnapshot{RunID: runID, Attempt: 1, Snapshot: stream.Snapshot{Stream: "stale", Candidate: "stale"}})
	t.Cleanup(stale.End)
	appendRunText(resumed, runID, "resumed", 1, 1, " text")
	require.Eventually(t, func() bool {
		return runState(t, h, f.memberToken, runID).Snapshot.CandidateContent == "resumed text"
	}, 5*time.Second, 20*time.Millisecond)
}
