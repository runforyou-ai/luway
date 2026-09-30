//go:build server

package agentrun

import (
	"errors"
	"testing"

	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
)

// testRunStreamDelta 构造指定起止序号的单操作增量。
func testRunStreamDelta(streamID string, sequence int64, operation runstream.Operation) runstream.Delta {
	return runstream.Delta{RunID: "run", StreamID: streamID, BaseSequence: sequence - 1, Sequence: sequence, Operations: []runstream.Operation{operation}}
}

// TestSubscribeRunStreamAfterRetry 验证重试替换执行尝试后订阅新流，旧尝试迟到的增量进不了新流。
func TestSubscribeRunStreamAfterRetry(t *testing.T) {
	previous := &runningAgentRun{attempt: 1, streamID: "stream-1", stream: runstream.NewHub(runstream.Snapshot{RunID: "run", StreamID: "stream-1", Attempt: 1})}
	action := &ExecuteAction{runningRuns: map[string]*runningAgentRun{"run": previous}}
	var staleDeltas, freshDeltas []runstream.Delta
	staleEnded := false
	stale, _, ok := action.SubscribeRunStream("run", func(delta runstream.Delta) { staleDeltas = append(staleDeltas, delta) }, func() { staleEnded = true })
	if !ok || stale.StreamID != "stream-1" {
		t.Fatalf("previous snapshot = %#v", stale)
	}
	action.runningRuns["run"] = &runningAgentRun{attempt: 2, streamID: "stream-2", stream: runstream.NewHub(runstream.Snapshot{RunID: "run", StreamID: "stream-2", Attempt: 2})}
	fresh, _, ok := action.SubscribeRunStream("run", func(delta runstream.Delta) { freshDeltas = append(freshDeltas, delta) }, func() {})
	if !ok || fresh.StreamID != "stream-2" || fresh.Attempt != 2 {
		t.Fatalf("current snapshot = %#v", fresh)
	}
	tail := testRunStreamDelta("stream-1", 1, runstream.Operation{Kind: runstream.OperationAppendCandidate, Text: "旧尝试尾部"})
	previous.stream.Publish(tail)
	previous.stream.End()
	if len(staleDeltas) != 1 || !staleEnded || len(freshDeltas) != 0 {
		t.Fatalf("stale deltas = %#v, stale ended = %t, fresh deltas = %#v", staleDeltas, staleEnded, freshDeltas)
	}
	if _, err := fresh.Apply(tail); !errors.Is(err, runstream.ErrMismatch) || fresh.CandidateContent != "" {
		t.Fatalf("apply previous tail error = %v, snapshot = %#v", err, fresh)
	}
}
