//go:build (darwin || ios) && cgo

package systemlocale

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#include <string.h>
#import <Foundation/Foundation.h>

static char* appPreferredLanguage(void) {
	NSArray<NSString*>* languages = [NSLocale preferredLanguages];
	if ([languages count] == 0) {
		return NULL;
	}
	return strdup([[languages firstObject] UTF8String]);
}
*/
import "C"

import "unsafe"

// preferredLanguage 返回 Apple 平台当前用户的首选系统语言。
func preferredLanguage() string {
	value := C.appPreferredLanguage()
	if value == nil {
		return preferredLanguageFromEnvironment()
	}
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}
