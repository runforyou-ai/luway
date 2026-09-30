package domain

// DevicePlatform 定义注册设备的运行平台。
type DevicePlatform string

const (
	DevicePlatformMacOS   DevicePlatform = "macos"
	DevicePlatformWindows DevicePlatform = "windows"
	DevicePlatformLinux   DevicePlatform = "linux"
)

// ValidDevicePlatform 判断设备平台是否为已知取值。
func ValidDevicePlatform(platform DevicePlatform) bool {
	switch platform {
	case DevicePlatformMacOS, DevicePlatformWindows, DevicePlatformLinux:
		return true
	}
	return false
}
