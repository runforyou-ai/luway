package timezone

import "testing"

// TestValid 验证常用 IANA 时区并拒绝无效名称。
func TestValid(t *testing.T) {
	for _, name := range []string{"UTC", "Asia/Shanghai", "America/New_York"} {
		if !Valid(name) {
			t.Fatalf("Valid(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "Local", " UTC ", "invalid"} {
		if Valid(name) {
			t.Fatalf("Valid(%q) = true, want false", name)
		}
	}
}
