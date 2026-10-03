//go:build !server && (android || ios)

package native

import "github.com/runforyou-ai/luway/internal/appservice"

// NewClientUpdater 禁用移动端应用内更新，移动端经应用商店更新。
func NewClientUpdater(ClientUpdateServer, func(bool)) appservice.ClientUpdater {
	return nil
}
