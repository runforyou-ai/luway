//go:build !server

package apiproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/cervi/internal/realtime/protocol"
)

// testLocalRuns 以一个运行流模拟本机执行中的运行。
type testLocalRuns struct {
	runID string
	hub   *runstream.Hub
}

// RunsLocally 判断是否为本机执行中的运行。
func (r testLocalRuns) RunsLocally(runID string) bool {
	return runID == r.runID
}

// SubscribeLocalRunStream 订阅本机运行流。
func (r testLocalRuns) SubscribeLocalRunStream(runID string, onDelta func(runstream.Delta), onEnd func()) (runstream.Snapshot, func(), bool) {
	if runID != r.runID {
		return runstream.Snapshot{}, nil, false
	}
	snapshot, subscription, ok := r.hub.Subscribe(onDelta, onEnd)
	if !ok {
		return runstream.Snapshot{}, nil, false
	}
	return snapshot, subscription.Close, true
}

// newLocalRunTestBackend 创建连接到按 allowed 决定阅读资格的服务器、并登记一条本机运行流的原生端后端，返回后端、事件、运行流与访问校验次数。
func newLocalRunTestBackend(t *testing.T, allowed bool) (*Backend, <-chan emittedEvent, *runstream.Hub, *atomic.Int32) {
	t.Helper()
	var checks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/agent-runs/run-1/stream-access" || request.Header.Get("Authorization") != "Bearer native-token" {
			http.NotFound(writer, request)
			return
		}
		checks.Add(1)
		if !allowed {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"error":{"kind":"not_found","message":"运行不存在"}}`))
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	backend, events := newRealtimeTestBackend(t, server.URL)
	hub := runstream.NewHub(runstream.Snapshot{RunID: "run-1", StreamID: "stream-1", Attempt: 1})
	backend.UseLocalRunStreams(testLocalRuns{runID: "run-1", hub: hub})
	return backend, events, hub, &checks
}

// localRunFrame 编码一条期望投递的本机运行过程事件。
func localRunFrame(t *testing.T, connectionID string, frame protocol.Frame) emittedEvent {
	t.Helper()
	data, err := protocol.Encode(frame)
	if err != nil {
		t.Fatal(err)
	}
	return emittedEvent{appservice.RealtimeRunFrameEventName, appservice.RealtimeRunFrameEvent{ConnectionID: connectionID, RunID: "run-1", Frame: string(data)}}
}

// TestLocalRunStreamDelivers 验证校验阅读资格后读取本机运行流，按快照、增量、结束与关闭的顺序投递。
func TestLocalRunStreamDelivers(t *testing.T) {
	backend, events, hub, checks := newLocalRunTestBackend(t, true)
	candidate := runstream.Operation{Kind: runstream.OperationAppendCandidate, Text: "处理中"}
	hub.Publish(runstream.Delta{RunID: "run-1", StreamID: "stream-1", Attempt: 1, BaseSequence: 0, Sequence: 1, Operations: []runstream.Operation{candidate}})

	connection, err := backend.ConnectAgentRunStream(context.Background(), appservice.RequestMeta{Locale: "zh-CN"}, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if checks.Load() != 1 {
		t.Fatalf("access checks = %d", checks.Load())
	}
	expectEvent(t, events, localRunFrame(t, connection.ConnectionID, protocol.RunStreamSnapshot{
		RunID: "run-1", StreamID: "stream-1", Attempt: 1, Sequence: 1, PartCount: 1, CandidateContent: "处理中", Blocks: []protocol.RunStreamBlock{},
	}))
	delta := runstream.Delta{RunID: "run-1", StreamID: "stream-1", Attempt: 1, BaseSequence: 1, Sequence: 2, Operations: []runstream.Operation{candidate}}
	hub.Publish(delta)
	expectEvent(t, events, localRunFrame(t, connection.ConnectionID, protocol.RunStreamDeltaFrame(delta)))
	hub.End()
	expectEvent(t, events, localRunFrame(t, connection.ConnectionID, protocol.RunStreamEnded{RunID: "run-1"}))
	expectEvent(t, events, emittedEvent{appservice.RealtimeRunClosedEventName, appservice.RealtimeRunClosedEvent{ConnectionID: connection.ConnectionID, RunID: "run-1"}})
}

// TestLocalRunStreamRequiresAccess 验证企业服务器拒绝阅读资格时不建立本机运行流。
func TestLocalRunStreamRequiresAccess(t *testing.T) {
	backend, events, _, checks := newLocalRunTestBackend(t, false)
	if _, err := backend.ConnectAgentRunStream(context.Background(), appservice.RequestMeta{Locale: "zh-CN"}, "run-1"); err == nil {
		t.Fatal("connected local run stream without access")
	}
	if checks.Load() != 1 || len(events) != 0 {
		t.Fatalf("access checks = %d, events = %d", checks.Load(), len(events))
	}
}

// TestLocalRunStreamDisconnect 验证断开本机运行流后投递关闭事件并取消订阅。
func TestLocalRunStreamDisconnect(t *testing.T) {
	backend, events, hub, _ := newLocalRunTestBackend(t, true)
	connection, err := backend.ConnectAgentRunStream(context.Background(), appservice.RequestMeta{Locale: "zh-CN"}, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	<-events
	if err := backend.DisconnectAgentRunStream(context.Background(), appservice.RequestMeta{}, connection.ConnectionID); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, events, emittedEvent{appservice.RealtimeRunClosedEventName, appservice.RealtimeRunClosedEvent{ConnectionID: connection.ConnectionID, RunID: "run-1"}})
	hub.Publish(runstream.Delta{RunID: "run-1", StreamID: "stream-1", Attempt: 1, BaseSequence: 0, Sequence: 1,
		Operations: []runstream.Operation{{Kind: runstream.OperationAppendCandidate, Text: "迟到"}}})
	if len(events) != 0 {
		t.Fatalf("events after disconnect = %d", len(events))
	}
}
