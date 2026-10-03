//go:build !server && ios

package storage

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#import <Foundation/Foundation.h>

// app_exclude_from_backup 为目录设置不进入 iCloud 与电脑备份的标记，成功返回 NULL，失败返回需由调用方释放的错误说明。
static char *app_exclude_from_backup(const char *path) {
	NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path] isDirectory:YES];
	NSError *error = nil;
	if ([url setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:&error]) {
		return NULL;
	}
	return strdup(error.localizedDescription.UTF8String ?: "unknown error");
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// excludeFromBackup 标记目录及其内容不进入 iCloud 与电脑备份。
func excludeFromBackup(directory string) error {
	path := C.CString(directory)
	defer C.free(unsafe.Pointer(path))
	message := C.app_exclude_from_backup(path)
	if message == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(message))
	return errors.New(C.GoString(message))
}
