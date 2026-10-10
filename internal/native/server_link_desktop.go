//go:build !server && ((darwin && !ios) || windows || (linux && !android))

package native

import "github.com/wailsapp/wails/v3/pkg/application"

// NewServerLinks 创建桌面端连接链接能力；系统打开链接时由组合根通过 Open 交来。
func NewServerLinks() (ServerLinks, []application.Service) {
	return &openedServerLink{}, nil
}
