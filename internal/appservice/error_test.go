package appservice

import "testing"

// TestMarshalErrorPreservesStructuredCause 验证 Wails 错误序列化保留业务字段。
func TestMarshalErrorPreservesStructuredCause(t *testing.T) {
	payload := MarshalError(&Error{
		Kind:    ErrorKindInvalid,
		Message: "输入有误。",
		Fields:  map[string]string{"name": "请输入名称。"},
	})
	if string(payload) != `{"kind":"invalid","message":"输入有误。","fields":{"name":"请输入名称。"}}` {
		t.Fatalf("payload = %s", payload)
	}
}

// TestMarshalErrorPreservesConflictReason 验证业务冲突原因使用独立字段传输。
func TestMarshalErrorPreservesConflictReason(t *testing.T) {
	payload := MarshalError(&Error{Kind: ErrorKindConflict, Message: "消息冲突。", Reason: "idempotency_mismatch"})
	if string(payload) != `{"kind":"conflict","message":"消息冲突。","reason":"idempotency_mismatch"}` {
		t.Fatalf("payload = %s", payload)
	}
}
