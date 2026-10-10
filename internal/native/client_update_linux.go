//go:build !server && linux && !android

package native

import (
	"context"
	"errors"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// linuxClientUpdater 读取当前服务器提供的客户端版本；Linux 客户端由 AppImage 或系统包管理器安装，不在应用内替换，较新时提示从下载页安装。
type linuxClientUpdater struct{}

// NewClientUpdater 创建 Linux 桌面端的新版本检查；未注入版本号的开发构建不检查。
func NewClientUpdater(*application.App) ClientUpdater {
	if buildinfo.Version == "dev" {
		return nil
	}
	return &linuxClientUpdater{}
}

// PrepareClientUpdate 读取服务器的更新清单，服务器提供的版本较新时返回 available，未连接服务器或没有更新时返回 current。
func (u *linuxClientUpdater) PrepareClientUpdate(ctx context.Context, meta appservice.RequestMeta, serverURL string) (ClientUpdate, error) {
	offered, err := readOfferedUpdate(ctx, serverURL, buildinfo.Version)
	if err != nil {
		slog.WarnContext(ctx, "检查客户端更新失败", "error", err)
		return ClientUpdate{}, appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, err)
	}
	if offered.NewerThan(buildinfo.Version) {
		return ClientUpdate{State: ClientUpdateStateAvailable, Version: offered.Version}, nil
	}
	return ClientUpdate{State: ClientUpdateStateCurrent}, nil
}

// RestartClientUpdate 在 Linux 上不可用，新版本由使用者从下载页安装。
func (u *linuxClientUpdater) RestartClientUpdate(_ context.Context, meta appservice.RequestMeta) error {
	return appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, errors.New("Linux 客户端不在应用内更新"))
}
