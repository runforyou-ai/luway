//go:build (darwin || ios) && !cgo

package systemlocale

// preferredLanguage 在 Apple 平台的无 CGO 服务端测试中读取消息语言环境变量。
func preferredLanguage() string {
	return preferredLanguageFromEnvironment()
}
