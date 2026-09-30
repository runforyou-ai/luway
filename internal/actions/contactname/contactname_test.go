//go:build server

package contactname

import "testing"

// TestNumber 验证从检索词末尾取出联系人编号。
func TestNumber(t *testing.T) {
	cases := map[string]int64{"12": 12, "#12": 12, "访客 #12": 12, "Visitor #12": 12, " 访客 # 7 ": 7}
	for query, want := range cases {
		if got := Number(query); got == nil || *got != want {
			t.Fatalf("Number(%q) = %v, want %d", query, got, want)
		}
	}
	for _, query := range []string{"", "abc12", "访客", "#", "12a", "99999999999999999999"} {
		if got := Number(query); got != nil {
			t.Fatalf("Number(%q) = %d, want nil", query, *got)
		}
	}
}
