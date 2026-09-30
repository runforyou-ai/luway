//go:build !server && android

package systemtray

import (
	"encoding/json"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// SetUnreadState 把未读总数交给原生通知桥接，角标由未读通知本身呈现。
func (*Controller) SetUnreadState(state appservice.UnreadIndicatorState) error {
	payload, err := json.Marshal(map[string]any{
		"action": "unread",
		"count":  state.Count,
	})
	if err != nil {
		return err
	}
	application.Android.Notify(string(payload))
	return nil
}
