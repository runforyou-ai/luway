package domain

import (
	"encoding/json"
	"testing"
)

// TestConversationChangesJSON 验证变化类别集合按固定顺序编码为名称数组，零值为空数组，未知名称解码失败。
func TestConversationChangesJSON(t *testing.T) {
	cases := []struct {
		changes ConversationChanges
		wire    string
	}{
		{0, `[]`},
		{ConversationChangeParticipants | ConversationChangeTimeline, `["timeline","participants"]`},
		{ConversationChangeTimeline | ConversationChangeService | ConversationChangeParticipants, `["timeline","service","participants"]`},
	}
	for _, current := range cases {
		data, err := json.Marshal(current.changes)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != current.wire {
			t.Fatalf("marshal %d = %s, want %s", current.changes, data, current.wire)
		}
		var decoded ConversationChanges
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded != current.changes {
			t.Fatalf("unmarshal %s = %d, want %d", data, decoded, current.changes)
		}
	}
	var decoded ConversationChanges
	if err := json.Unmarshal([]byte(`["unknown"]`), &decoded); err == nil {
		t.Fatal("unknown change decoded without error")
	}
}

// TestConversationSystemEventChanges 验证群事件归入参与方变化，客户会话事件归入服务周期变化。
func TestConversationSystemEventChanges(t *testing.T) {
	if got := ConversationSystemEventGroupMembersAdded.ConversationChanges(); got != ConversationChangeParticipants {
		t.Fatalf("group event changes = %d", got)
	}
	if got := ConversationSystemEventServiceSessionClosed.ConversationChanges(); got != ConversationChangeService {
		t.Fatalf("service event changes = %d", got)
	}
	if got := ConversationSystemEventServiceStatusChanged.ConversationChanges(); got != ConversationChangeService {
		t.Fatalf("service status changes = %d", got)
	}
}
