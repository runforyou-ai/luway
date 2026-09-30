//go:build server

package customernotify

import "testing"

// TestExtractEmail 验证只识别正文中唯一的合法邮箱。
func TestExtractEmail(t *testing.T) {
	cases := []struct {
		body  string
		email string
		found bool
	}{
		{"我的邮箱是 Visitor@Example.com。", "visitor@example.com", true},
		{"email: a@b.co, thanks!", "a@b.co", true},
		{"（a@b.co）", "a@b.co", true},
		{"a@b.co 和 A@B.CO", "a@b.co", true},
		{"a@b.co 或 c@d.co", "", false},
		{"没有邮箱", "", false},
		{"@someone 看一下", "", false},
	}
	for _, item := range cases {
		email, found := extractEmail(item.body)
		if email != item.email || found != item.found {
			t.Errorf("extractEmail(%q) = %q, %t", item.body, email, found)
		}
	}
}
