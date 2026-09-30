package common

import "testing"

// TestContainsPattern 验证关键词中的通配符和转义符按字面匹配。
func TestContainsPattern(t *testing.T) {
	if got := ContainsPattern(`50%_off\`); got != `%50\%\_off\\%` {
		t.Fatalf("ContainsPattern() = %q", got)
	}
}
