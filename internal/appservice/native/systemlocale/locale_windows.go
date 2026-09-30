//go:build windows

package systemlocale

import (
	"syscall"
	"unsafe"
)

const muiLanguageName = 0x8

var getUserPreferredUILanguages = syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserPreferredUILanguages")

// preferredLanguage 返回 Windows 当前用户的首选界面语言。
func preferredLanguage() string {
	var languageCount uint32
	var bufferLength uint32
	_, _, _ = getUserPreferredUILanguages.Call(
		muiLanguageName,
		uintptr(unsafe.Pointer(&languageCount)),
		0,
		uintptr(unsafe.Pointer(&bufferLength)),
	)
	if bufferLength == 0 {
		return preferredLanguageFromEnvironment()
	}

	buffer := make([]uint16, bufferLength)
	result, _, _ := getUserPreferredUILanguages.Call(
		muiLanguageName,
		uintptr(unsafe.Pointer(&languageCount)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&bufferLength)),
	)
	if result == 0 {
		return preferredLanguageFromEnvironment()
	}
	return syscall.UTF16ToString(buffer)
}
