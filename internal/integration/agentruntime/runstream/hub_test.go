package runstream

import (
	"errors"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
)

// testHubDelta 构造指定起止序号的单操作增量。
func testHubDelta(streamID string, sequence int64, operation Operation) Delta {
	return Delta{RunID: "run", StreamID: streamID, BaseSequence: sequence - 1, Sequence: sequence, Operations: []Operation{operation}}
}

// TestStreamHubSubscription 验证订阅按序接收增量、漏收后重新订阅恢复全文、取消与结束后停止回调。
func TestStreamHubSubscription(t *testing.T) {
	hub := NewHub(Snapshot{RunID: "run", StreamID: "stream", Attempt: 1})
	appendText := Operation{Kind: OperationAppendBlockText, BlockID: "thinking", Text: "1"}
	hub.Publish(testHubDelta("stream", 1, Operation{Kind: OperationUpsertBlock, Block: &Block{ID: "thinking", Position: 1, Kind: domain.AgentRunBlockThinking, Text: "0"}}))
	var received []Delta
	ended := 0
	snapshot, subscription, ok := hub.Subscribe(func(delta Delta) { received = append(received, delta) }, func() { ended++ })
	if !ok || snapshot.Sequence != 1 || snapshot.Blocks[0].Text != "0" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	hub.Publish(testHubDelta("stream", 2, appendText))
	hub.Publish(testHubDelta("stream", 3, appendText))
	if len(received) != 2 || received[0].Sequence != 2 || received[1].Sequence != 3 {
		t.Fatalf("received = %#v", received)
	}
	// 漏收一条增量后应用出现缺口，重新订阅从快照恢复全文。
	if _, err := snapshot.Apply(received[1]); !errors.Is(err, ErrGap) {
		t.Fatalf("apply after missing delta error = %v", err)
	}
	resumed, later, ok := hub.Subscribe(func(Delta) {}, func() {})
	if !ok || resumed.Sequence != 3 || resumed.Blocks[0].Text != "011" {
		t.Fatalf("resumed snapshot = %#v", resumed)
	}
	resumed.Blocks[0].Text = "changed"
	if copied, _, _ := hub.Subscribe(func(Delta) {}, func() {}); copied.Blocks[0].Text != "011" {
		t.Fatalf("snapshot shared with subscriber: %#v", copied)
	}
	later.Close()
	subscription.Close()
	hub.Publish(testHubDelta("stream", 4, appendText))
	if len(received) != 2 {
		t.Fatalf("closed subscription received = %#v", received)
	}
	_, active, _ := hub.Subscribe(func(Delta) {}, func() { ended++ })
	hub.End()
	hub.End()
	if ended != 1 {
		t.Fatalf("end callbacks = %d", ended)
	}
	active.Close()
	if _, _, ok := hub.Subscribe(func(Delta) {}, func() {}); ok {
		t.Fatal("subscribed to ended stream")
	}
}

// TestStreamHubEndsOnInvalidDelta 验证增量无法应用时结束运行流并通知订阅方。
func TestStreamHubEndsOnInvalidDelta(t *testing.T) {
	hub := NewHub(Snapshot{RunID: "run", StreamID: "stream"})
	delivered, ended := 0, 0
	hub.Subscribe(func(Delta) { delivered++ }, func() { ended++ })
	hub.Publish(testHubDelta("stream", 2, Operation{Kind: OperationClearCandidate}))
	if delivered != 0 || ended != 1 {
		t.Fatalf("delivered = %d, ended = %d", delivered, ended)
	}
	if _, _, ok := hub.Subscribe(func(Delta) {}, func() {}); ok {
		t.Fatal("subscribed to ended stream")
	}
}
