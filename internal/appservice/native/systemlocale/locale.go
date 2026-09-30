// Package systemlocale 识别原生端当前用户的首选系统语言。
package systemlocale

import (
	"os"
	"strings"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"golang.org/x/text/language"
)

// Detect 把系统首选语言归一化为应用支持的语言。
func Detect() appservice.Locale {
	return resolve(preferredLanguage())
}

// resolve 把操作系统语言值映射为应用支持的语言。
func resolve(value string) appservice.Locale {
	// 把操作系统常见语言格式转换成 BCP 47 标签。
	value = strings.TrimSpace(value)
	if index := strings.IndexAny(value, ".@"); index >= 0 {
		value = value[:index]
	}
	if value == "C" || value == "POSIX" || value == "" {
		value = "en-US"
	} else {
		value = strings.ReplaceAll(value, "_", "-")
	}
	tag, err := language.Parse(value)
	if err != nil {
		return appservice.LocaleEnglishUnitedStates
	}
	base, _ := tag.Base()
	if base.String() == "en" {
		return appservice.LocaleEnglishUnitedStates
	}
	if base.String() == "zh" {
		script, _ := tag.Script()
		if script.String() == "Hans" {
			return appservice.LocaleChineseSimplified
		}
	}
	return appservice.LocaleEnglishUnitedStates
}

// preferredLanguageFromEnvironment 按消息语言环境变量读取首选语言。
func preferredLanguageFromEnvironment() string {
	if value := strings.TrimSpace(os.Getenv("LANGUAGE")); value != "" {
		return strings.Split(value, ":")[0]
	}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
