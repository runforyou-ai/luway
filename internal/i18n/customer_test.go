package i18n

import (
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestCustomerLocalesProvideCustomerKeys 验证每种对客语言都完整提供对客词条、只含对客词条，且只使用应用语言词条中出现过的占位符。
func TestCustomerLocalesProvideCustomerKeys(t *testing.T) {
	placeholder := regexp.MustCompile(`\{\{\.\w+\}\}|\{\w+\}`)
	// 汇总对客词条及其在应用语言词条中出现过的占位符。
	customerKeys := make(map[string][]string)
	for _, path := range appLocaleFiles {
		for key, message := range readLocaleMessages(t, path) {
			if isCustomerKey(key) {
				customerKeys[key] = append(customerKeys[key], placeholder.FindAllString(message, -1)...)
			}
		}
	}
	if len(customerKeys) == 0 {
		t.Fatal("应用语言词条中没有对客词条")
	}
	for _, locale := range domain.CustomerLocales {
		path := customerLocaleFile(locale)
		messages := readLocaleMessages(t, path)
		var missing, mismatched []string
		for key, allowed := range customerKeys {
			message, ok := messages[key]
			if !ok || strings.TrimSpace(message) == "" {
				missing = append(missing, key)
				continue
			}
			for _, name := range placeholder.FindAllString(message, -1) {
				if !slices.Contains(allowed, name) {
					mismatched = append(mismatched, key)
					break
				}
			}
		}
		sort.Strings(missing)
		sort.Strings(mismatched)
		if len(missing) > 0 {
			t.Errorf("%s 缺少对客词条: %v", path, missing)
		}
		if len(mismatched) > 0 {
			t.Errorf("%s 使用了应用语言词条中没有的占位符: %v", path, mismatched)
		}
		if strings.HasPrefix(path, "locales/customer/") {
			var extra []string
			for key := range messages {
				if _, ok := customerKeys[key]; !ok {
					extra = append(extra, key)
				}
			}
			sort.Strings(extra)
			if len(extra) > 0 {
				t.Errorf("%s 包含对客词条以外的词条: %v", path, extra)
			}
		}
	}
}

// TestMatchCustomerLocale 验证按语言偏好顺序匹配对客语言。
func TestMatchCustomerLocale(t *testing.T) {
	tests := []struct {
		languages string
		want      domain.CustomerLocale
		wantOK    bool
	}{
		{languages: "hi-IN,hi;q=0.9,en;q=0.8", want: domain.CustomerLocaleHindiIndia, wantOK: true},
		{languages: "hi", want: domain.CustomerLocaleHindiIndia, wantOK: true},
		{languages: "en-IN", want: domain.CustomerLocaleEnglishUnitedStates, wantOK: true},
		{languages: "zh-TW", want: domain.CustomerLocaleChineseSimplified, wantOK: true},
		{languages: "zh-Hant-TW", want: domain.CustomerLocaleChineseSimplified, wantOK: true},
		{languages: "hi-Deva", want: domain.CustomerLocaleHindiIndia, wantOK: true},
		{languages: "hi-Latn", wantOK: false},
		{languages: "hi-Latn,en;q=0.8", want: domain.CustomerLocaleEnglishUnitedStates, wantOK: true},
		{languages: "fr-FR,hi;q=0.8,en;q=0.5", want: domain.CustomerLocaleHindiIndia, wantOK: true},
		{languages: "en;q=0.5,hi;q=0.9", want: domain.CustomerLocaleHindiIndia, wantOK: true},
		{languages: "fr-FR", wantOK: false},
		{languages: "und", wantOK: false},
		{languages: "", wantOK: false},
	}
	for _, test := range tests {
		got, ok := MatchCustomerLocale(test.languages)
		if got != test.want || ok != test.wantOK {
			t.Errorf("MatchCustomerLocale(%q) = (%q, %v), want (%q, %v)", test.languages, got, ok, test.want, test.wantOK)
		}
	}
}

// TestPreferredCustomerLocale 验证无匹配的语言偏好回退到英文。
func TestPreferredCustomerLocale(t *testing.T) {
	for _, languages := range []string{"ja-JP,ja;q=0.9", "hi-Latn", ""} {
		if got := PreferredCustomerLocale(languages); got != domain.CustomerLocaleEnglishUnitedStates {
			t.Errorf("PreferredCustomerLocale(%q) = %q, want en-US", languages, got)
		}
	}
}

// TestLocalizeCustomerTemplate 验证对客文案按对客语言渲染，应用语言不包含只用于对客的语言。
func TestLocalizeCustomerTemplate(t *testing.T) {
	if got := LocalizeCustomerTemplate(domain.CustomerLocaleHindiIndia, MessengerSend, nil); got != "भेजें" {
		t.Fatalf("印地语对客文案 = %q", got)
	}
	if _, matched := Localize("hi-IN", ErrorAuthenticationRequired); matched != "en-US" {
		t.Fatalf("应用语言匹配到 %q，want en-US", matched)
	}
}

// readLocaleMessages 读取指定语言文件的全部词条。
func readLocaleMessages(t *testing.T, path string) map[string]string {
	t.Helper()
	content, err := localeFiles.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	messages := make(map[string]string)
	if err := json.Unmarshal(content, &messages); err != nil {
		t.Fatal(err)
	}
	return messages
}

// isCustomerKey 判断词条是否为对客词条。
func isCustomerKey(key string) bool {
	if strings.HasPrefix(key, customerExcludedKeyPrefix) {
		return false
	}
	for _, prefix := range customerKeyPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
