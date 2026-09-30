package protocol

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
)

// TestRunStreamSnapshotParts 验证快照按文本预算拆分，候选正文只在首个分片，每个分片至少一个内容块。
func TestRunStreamSnapshotParts(t *testing.T) {
	startedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	snapshot := runstream.Snapshot{
		RunID: "run-1", StreamID: "stream-1", Attempt: 2, Sequence: 9, CandidateContent: "候选",
		Blocks: []runstream.Block{
			{ID: "block-1", Position: 1, Kind: domain.AgentRunBlockThinking, Text: strings.Repeat("a", 10)},
			{ID: "block-2", Position: 2, Kind: domain.AgentRunBlockThinking, Text: strings.Repeat("b", 10)},
			{ID: "block-3", Position: 3, Kind: domain.AgentRunBlockToolCall, ToolCall: &runstream.ToolCall{
				Name: "search_knowledge", Status: domain.AgentToolCallRunning, StartedAt: &startedAt,
			}},
		},
	}
	parts := RunStreamSnapshotParts(snapshot, 12)
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	for i, part := range parts {
		if part.Part != i || part.PartCount != 2 || part.RunID != "run-1" || part.StreamID != "stream-1" || part.Attempt != 2 || part.Sequence != 9 {
			t.Fatalf("part %d = %#v", i, part)
		}
	}
	if parts[0].CandidateContent != "候选" || parts[1].CandidateContent != "" {
		t.Fatalf("candidate = %q / %q", parts[0].CandidateContent, parts[1].CandidateContent)
	}
	if len(parts[0].Blocks) != 1 || parts[0].Blocks[0].ID != "block-1" {
		t.Fatalf("first part blocks = %#v", parts[0].Blocks)
	}
	if len(parts[1].Blocks) != 2 || parts[1].Blocks[1].ToolCall == nil || parts[1].Blocks[1].ToolCall.Name != "search_knowledge" {
		t.Fatalf("second part blocks = %#v", parts[1].Blocks)
	}
	// 工具调用只携带名称、状态和起止时间。
	if parts[1].Blocks[1].ToolCall.Status != domain.AgentToolCallRunning || !parts[1].Blocks[1].ToolCall.StartedAt.Equal(startedAt) {
		t.Fatalf("tool call = %#v", parts[1].Blocks[1].ToolCall)
	}
}

// TestRunStreamSnapshotPartsEmpty 验证空快照仍拆分为一个分片。
func TestRunStreamSnapshotPartsEmpty(t *testing.T) {
	parts := RunStreamSnapshotParts(runstream.Snapshot{RunID: "run-1", StreamID: "stream-1"}, 16)
	want := []RunStreamSnapshot{{RunID: "run-1", StreamID: "stream-1", PartCount: 1, Blocks: []RunStreamBlock{}}}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
}
