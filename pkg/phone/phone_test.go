package phone

import "testing"

// TestNormalize 验证国际电话号码标准化与校验规则。
func TestNormalize(t *testing.T) {
	for input, want := range map[string]string{
		"+1 (415) 555-0100":   "+14155550100",
		" +86 138 0000 0000 ": "+8613800000000",
	} {
		if got, ok := Normalize(input); !ok || got != want {
			t.Fatalf("Normalize(%q) = %q, %v, want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "4155550100", "+12345", "+1 415 555 01OO", "+1234567890123456"} {
		if got, ok := Normalize(input); ok {
			t.Fatalf("Normalize(%q) = %q, want invalid", input, got)
		}
	}
}
