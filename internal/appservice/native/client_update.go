//go:build !server

package native

import (
	"context"

	"github.com/runforyou-ai/luway/internal/appservice"
)

// ClientUpdateServer 是应用内更新使用的服务器连接：已保存的服务器地址与服务器状态。
type ClientUpdateServer interface {
	ServerURL(context.Context, appservice.RequestMeta) (string, error)
	InstallationStatus(context.Context, appservice.RequestMeta) (appservice.InstallationStatus, error)
}
