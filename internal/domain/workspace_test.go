package domain

import (
	"strings"
	"testing"
)

// TestWorkspaceSlugValid 验证工作区标识的字符与长度规则。
func TestWorkspaceSlugValid(t *testing.T) {
	for slug, want := range map[string]bool{
		"acme":                  true,
		"a":                     true,
		"acme-01":               true,
		"0acme":                 true,
		strings.Repeat("a", 63): true,
		strings.Repeat("a", 64): false,
		"":                      false,
		"-acme":                 false,
		"acme-":                 false,
		"ac_me":                 false,
		"ac.me":                 false,
		"Acme":                  false,
		"中文":                    false,
	} {
		if got := WorkspaceSlugValid(slug); got != want {
			t.Fatalf("WorkspaceSlugValid(%q) = %v", slug, got)
		}
	}
	if got := NormalizeWorkspaceSlug("  AcMe "); got != "acme" {
		t.Fatalf("NormalizeWorkspaceSlug = %q", got)
	}
}
