//go:build !windows && !darwin && !ios

package systemlocale

// preferredLanguage 返回 Unix 类系统的首选消息语言。
func preferredLanguage() string {
	return preferredLanguageFromEnvironment()
}
