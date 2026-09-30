//go:build server

package publicweb

import "testing"

// TestParseTheme 验证主题对比色、主题文字色和焦点色。
func TestParseTheme(t *testing.T) {
	cases := []struct {
		input   string
		color   string
		onColor string
		text    string
		focus   string
	}{
		{"#2563EB", "#2563EB", "#FFFFFF", "#2563EB", "rgba(37, 99, 235, 0.40)"},
		{"#2563eb", "#2563EB", "#FFFFFF", "#2563EB", "rgba(37, 99, 235, 0.40)"},
		{"not-a-color", "#2563EB", "#FFFFFF", "#2563EB", "rgba(37, 99, 235, 0.40)"},
		{"#FDE68A", "#FDE68A", "#1C1917", "#81754B", "rgba(28, 25, 23, 0.35)"},
		{"#FFFFFF", "#FFFFFF", "#1C1917", "#82817F", "rgba(28, 25, 23, 0.35)"},
		{"#000000", "#000000", "#FFFFFF", "#000000", "rgba(0, 0, 0, 0.40)"},
		{"#EA580C", "#EA580C", "#1C1917", "#793512", "rgba(234, 88, 12, 0.40)"},
		{"#16A34A", "#16A34A", "#1C1917", "#19572E", "rgba(22, 163, 74, 0.40)"},
		{"#006EFA", "#006EFA", "#FFFFFF", "#006EFA", "rgba(0, 110, 250, 0.40)"},
	}
	for _, test := range cases {
		theme := parseTheme(test.input)
		if theme.Color != test.color || theme.OnColor != test.onColor || theme.Text != test.text || theme.Focus != test.focus {
			t.Fatalf(
				"parseTheme(%q) = %+v, want color=%s on=%s text=%s focus=%s",
				test.input,
				theme,
				test.color,
				test.onColor,
				test.text,
				test.focus,
			)
		}
	}
}
