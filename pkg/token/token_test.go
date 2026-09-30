package token

import (
	"testing"
	"time"
)

// TestIssue 验证令牌哈希与明文对应，过期时间按传入的有效期计算。
func TestIssue(t *testing.T) {
	const validity = 7 * 24 * time.Hour
	issued, err := Issue(validity)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.TokenHash != Hash(issued.Token) {
		t.Fatalf("unexpected issued token: %#v", issued)
	}
	remaining := time.Until(issued.ExpiresAt)
	if remaining < validity-time.Minute || remaining > validity {
		t.Fatalf("token duration = %v, want about %v", remaining, validity)
	}
}
