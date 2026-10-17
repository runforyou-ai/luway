package brand

import (
	"strings"
	"testing"
)

func TestValidateRejectsShellMetacharacters(t *testing.T) {
	base := Build()
	for _, name := range []string{`$(printf x)`, "`id`", `a"b`, `a\b`, "a\nb"} {
		invalid := base
		invalid.Names = map[string]string{DefaultLocale: name}
		if err := invalid.Validate(); err == nil {
			t.Fatalf("expected reject for name %q", name)
		}
	}
	invalid := base
	invalid.Description = "ok; rm -rf /"
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected reject for description with semicolon")
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("official brand must remain valid: %v", err)
	}
	if strings.Contains(base.DisplayName(), "$") {
		t.Fatal("sanity")
	}
}
