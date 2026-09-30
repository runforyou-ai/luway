package systemlocale

import (
	"testing"

	"github.com/runforyou-ai/cervi/internal/appservice"
)

// TestResolve 验证系统语言值与应用语言的映射规则。
func TestResolve(t *testing.T) {
	tests := []struct {
		value string
		want  appservice.Locale
	}{
		{value: "zh_CN.UTF-8", want: appservice.LocaleChineseSimplified},
		{value: "zh-Hans", want: appservice.LocaleChineseSimplified},
		{value: "zh-Hant", want: appservice.LocaleEnglishUnitedStates},
		{value: "en_GB.UTF-8", want: appservice.LocaleEnglishUnitedStates},
		{value: "fr-FR", want: appservice.LocaleEnglishUnitedStates},
		{value: "C.UTF-8", want: appservice.LocaleEnglishUnitedStates},
	}

	for _, test := range tests {
		if got := resolve(test.value); got != test.want {
			t.Fatalf("resolve(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}
