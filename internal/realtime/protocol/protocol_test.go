package protocol

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
)

var (
	runStreamRunID     = "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b"
	runStreamStreamID  = "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8c"
	runStreamStarted   = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	runStreamCompleted = time.Date(2026, 9, 15, 12, 0, 3, 0, time.UTC)
)

// fixtureCase 是 Go 与 TypeScript 共用的事件夹具样例。
type fixtureCase struct {
	Name   string          `json:"name"`
	Result string          `json:"result"`
	Encode bool            `json:"encode"`
	Wire   json.RawMessage `json:"wire"`
}

// expectedFrames 是结果为 frame 的夹具样例在 Go 端解码后应得到的事件。
var expectedFrames = map[string]Frame{
	"server_hello":                      ServerHello{ConnectionID: "conn-01", SyncHeads: appservice.SyncHeads{ConversationCount: 3, ConversationChecksum: "18446744073709551615", IdentityProfileVersion: "9223372036854775807", PinOrderVersion: "12"}},
	"visitor_hello":                     VisitorHello{ConnectionID: "conn-02"},
	"ping":                              Ping{},
	"conversation_changed":              ConversationChanged{ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b", ConversationType: domain.ConversationTypeGroup, Version: 9223372036854775807, Changes: domain.ConversationChangeTimeline | domain.ConversationChangeParticipants},
	"conversation_state_changed":        ConversationStateChanged{ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b", Version: 42},
	"identity_profile_changed":          IdentityProfileChanged{Version: 9007199254740993},
	"pin_order_changed":                 PinOrderChanged{Version: 5},
	"service_attention":                 ServiceAttention{ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b", ServiceSessionID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8e", Reason: domain.ServiceAttentionAssigned},
	"conversation_typing":               ConversationTyping{ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b", SenderSubjectID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8d", Active: true},
	"conversation_typing_stopped":       ConversationTyping{ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b", SenderSubjectID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8d"},
	"visitor_typing":                    VisitorTyping{ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b", Active: true},
	"reception_changed":                 ReceptionChanged{},
	"knowledge_gaps_changed":            KnowledgeGapsChanged{},
	"service_reports_changed":           ServiceReportsChanged{},
	"conversation_removed":              ConversationRemoved{ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b"},
	"conversation_changed_extra_fields": ConversationChanged{ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b", ConversationType: domain.ConversationTypeChannel, Version: 7},
	"ping_without_data":                 Ping{},
	"run_stream_snapshot": RunStreamSnapshot{
		RunID: runStreamRunID, StreamID: runStreamStreamID, Attempt: 1, Sequence: 7, Part: 0, PartCount: 2,
		CandidateContent: "根据知识库的记录，",
		Plan: []RunStreamPlanTask{
			{ID: "1", Subject: "核对退款政策", ActiveForm: "正在核对退款政策", Status: domain.AgentPlanTaskInProgress},
			{ID: "2", Subject: "整理答复", Status: domain.AgentPlanTaskPending},
		},
		Blocks: []RunStreamBlock{
			{ID: "block-1", Position: 1, Kind: domain.AgentRunBlockThinking, Text: "先确认退款政策"},
			{ID: "block-2", Position: 2, Kind: domain.AgentRunBlockToolCall, ToolCall: &RunStreamToolCall{
				Name: "search_knowledge", Status: domain.AgentToolCallRunning, StartedAt: &runStreamStarted,
			}},
			{ID: "block-3", Position: 3, Kind: domain.AgentRunBlockToolCall, ToolCall: &RunStreamToolCall{
				Name: "agent", Status: domain.AgentToolCallRunning, StartedAt: &runStreamStarted, Description: "查询历史订单", Activity: "web_search",
			}},
		},
	},
	"run_stream_snapshot_empty": RunStreamSnapshot{
		RunID: runStreamRunID, StreamID: runStreamStreamID, Attempt: 1, Part: 0, PartCount: 1, Blocks: []RunStreamBlock{},
	},
	"run_stream_delta": RunStreamDelta{
		RunID: runStreamRunID, StreamID: runStreamStreamID, Attempt: 1, BaseSequence: 7, Sequence: 9,
		Operations: []RunStreamOperation{
			{Kind: RunStreamAppendBlockText, BlockID: "block-1", Text: "，再给出答复"},
			{Kind: RunStreamUpsertBlock, Block: &RunStreamBlock{ID: "block-2", Position: 2, Kind: domain.AgentRunBlockToolCall, ToolCall: &RunStreamToolCall{
				Name: "search_knowledge", Status: domain.AgentToolCallSucceeded, StartedAt: &runStreamStarted, CompletedAt: &runStreamCompleted,
			}}},
			{Kind: RunStreamRemoveBlocks, BlockIDs: []string{"block-3"}},
			{Kind: RunStreamClearCandidate},
			{Kind: RunStreamAppendCandidate, Text: "退款需要在 7 天内提交。"},
			{Kind: RunStreamSetPlan, Plan: []RunStreamPlanTask{{ID: "1", Subject: "核对退款政策", Status: domain.AgentPlanTaskCompleted}}},
		},
	},
	"run_stream_ended":         RunStreamEnded{RunID: runStreamRunID},
	"device_work_advanced":     DeviceWorkAdvanced{DeviceID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a90", WorkSeq: 9223372036854775807},
	"assistant_memory_changed": AssistantMemoryChanged{AssistantID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a91"},
	"workspace_activity": WorkspaceActivity{
		WorkspaceID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a92", Kind: TypeConversationChanged,
		ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b", Changes: domain.ConversationChangeTimeline,
	},
	"workspace_activity_service_attention": WorkspaceActivity{
		WorkspaceID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a92", Kind: TypeServiceAttention, ConversationID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b",
		ServiceSessionID: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a93", Reason: domain.ServiceAttentionAssigned,
	},
}

// TestFrameFixtures 按共用夹具校验 Go 端解码结果，并校验编码输出与夹具线上格式一致。
func TestFrameFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/frames.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}

	encodedTypes := map[Type]bool{}
	usedFrames := map[string]bool{}
	for _, fixture := range cases {
		t.Run(fixture.Name, func(t *testing.T) {
			frame, err := Decode(fixture.Wire)
			switch fixture.Result {
			case "frame":
				expected, ok := expectedFrames[fixture.Name]
				if !ok {
					t.Fatalf("missing expected frame")
				}
				usedFrames[fixture.Name] = true
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !reflect.DeepEqual(frame, expected) {
					t.Fatalf("decoded %#v, want %#v", frame, expected)
				}
				if !fixture.Encode {
					return
				}
				encodedTypes[frame.FrameType()] = true
				encoded, err := Encode(expected)
				if err != nil {
					t.Fatalf("encode: %v", err)
				}
				var got, want any
				if err := json.Unmarshal(encoded, &got); err != nil {
					t.Fatalf("parse encoded frame: %v", err)
				}
				if err := json.Unmarshal(fixture.Wire, &want); err != nil {
					t.Fatalf("parse fixture wire: %v", err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("encoded %s, want %s", encoded, fixture.Wire)
				}
			case "ignored":
				if !errors.Is(err, ErrUnknownFrame) {
					t.Fatalf("decode error %v, want ErrUnknownFrame", err)
				}
			case "unsupported_version":
				if !errors.Is(err, ErrUnsupportedVersion) {
					t.Fatalf("decode error %v, want ErrUnsupportedVersion", err)
				}
			case "invalid":
				if err == nil || errors.Is(err, ErrUnknownFrame) || errors.Is(err, ErrUnsupportedVersion) {
					t.Fatalf("decode error %v, want invalid frame error", err)
				}
			default:
				t.Fatalf("unknown fixture result %q", fixture.Result)
			}
		})
	}

	// 每种事件都至少有一个编码往返样例，每个期望事件都对应夹具样例。
	for frameType := range decoders {
		if !encodedTypes[frameType] {
			t.Errorf("frame %s has no encode fixture", frameType)
		}
	}
	for name := range expectedFrames {
		if !usedFrames[name] {
			t.Errorf("expected frame %s has no fixture", name)
		}
	}
}
