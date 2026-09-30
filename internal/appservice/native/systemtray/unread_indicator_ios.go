//go:build !server && ios

package systemtray

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework UIKit -framework UserNotifications
#include "unread_indicator_ios.h"
*/
import "C"

import (
	"github.com/runforyou-ai/luway/internal/appservice"
)

// SetUnreadState 更新 iOS 应用图标角标。
func (*Controller) SetUnreadState(state appservice.UnreadIndicatorState) error {
	C.app_unread_set_badge(C.int(state.Count))
	return nil
}
