package email

import "testing"

// TestNormalize 验证邮箱地址标准化规则。
func TestNormalize(t *testing.T) {
	if result := Normalize("  Admin@Example.COM "); result != "admin@example.com" {
		t.Fatalf("Normalize() = %q, want %q", result, "admin@example.com")
	}
}

// TestValid 验证邮箱地址格式校验。
func TestValid(t *testing.T) {
	for _, value := range []string{"admin@example.com", "team.member@example.com"} {
		if !Valid(value) {
			t.Fatalf("Valid(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "admin", "Admin <admin@example.com>"} {
		if Valid(value) {
			t.Fatalf("Valid(%q) = true, want false", value)
		}
	}
}
