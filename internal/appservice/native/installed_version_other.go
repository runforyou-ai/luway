//go:build !server && ((darwin && !ios) || (linux && !android))

package native

// syncInstalledVersion 在 macOS 与 Linux 上无需同步：系统从应用包或包管理器读取版本。
func syncInstalledVersion() {}
