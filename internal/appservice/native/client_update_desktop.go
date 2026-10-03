//go:build !server && ((darwin && !ios) || windows)

package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
)

// selfUpdater 是 Wails 更新器中检查、下载与重启的部分。
type selfUpdater interface {
	Check(context.Context) (*updater.Release, error)
	DownloadAndInstall(context.Context) error
	Restart(context.Context) error
}

// clientUpdater 经 Wails 更新器从当前连接的服务器下载、校验并安装桌面端新版本。
type clientUpdater struct {
	updater selfUpdater
	// allowQuit 在重启更新前放行应用退出。
	allowQuit func(bool)
	mu        sync.Mutex
	// ready 是已下载并通过签名校验的版本。
	ready string
}

// NewClientUpdater 创建桌面端更新能力；serverURL 读取当前连接的服务器地址，allowQuit 控制托盘常驻的应用能否退出。未注入版本号的开发构建不更新。
func NewClientUpdater(app *application.App, serverURL func(context.Context) (string, error), allowQuit func(bool)) appservice.ClientUpdater {
	if buildinfo.Version == "dev" {
		return nil
	}
	if err := app.Updater.Init(updaterConfig(buildinfo.Version, brand.Build().UpdateKey(), serverURL)); err != nil {
		slog.Warn("初始化客户端更新失败", "error", err)
		return nil
	}
	return &clientUpdater{updater: app.Updater, allowQuit: allowQuit}
}

// updaterConfig 返回从当前服务器读取更新清单、用品牌公钥校验签名且不打开更新窗口的更新器配置。
func updaterConfig(version string, publicKey []byte, serverURL func(context.Context) (string, error)) updater.Config {
	return updater.Config{
		CurrentVersion: version,
		Providers:      []updater.Provider{&serverProvider{serverURL: serverURL}},
		PublicKey:      publicKey,
		Window:         updater.WindowNone,
	}
}

// PrepareClientUpdate 检查当前服务器提供的客户端版本，较新时下载更新包并校验签名，同一版本只下载一次。
func (u *clientUpdater) PrepareClientUpdate(ctx context.Context, meta appservice.RequestMeta) (appservice.ClientUpdate, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	release, err := u.updater.Check(ctx)
	if err != nil {
		slog.Warn("检查客户端更新失败", "error", err)
		return appservice.ClientUpdate{}, appservice.FailedError(meta, i18n.ErrorClientUpdateFailed)
	}
	if release == nil {
		u.ready = ""
		return appservice.ClientUpdate{State: appservice.ClientUpdateStateCurrent}, nil
	}
	if release.Version != u.ready {
		u.ready = ""
		if err := u.updater.DownloadAndInstall(ctx); err != nil {
			slog.Warn("下载客户端更新失败", "version", release.Version, "error", err)
			return appservice.ClientUpdate{}, appservice.FailedError(meta, i18n.ErrorClientUpdateFailed)
		}
		u.ready = release.Version
		slog.Info("客户端新版本已就绪", "version", release.Version)
	}
	return appservice.ClientUpdate{State: appservice.ClientUpdateStateReady, Version: u.ready}, nil
}

// RestartClientUpdate 放行应用退出后交给 Wails 更新器替换程序并重新启动。
func (u *clientUpdater) RestartClientUpdate(ctx context.Context, meta appservice.RequestMeta) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ready == "" {
		return appservice.FailedError(meta, i18n.ErrorClientUpdateFailed)
	}
	u.allowQuit(true)
	if err := u.updater.Restart(ctx); err != nil {
		u.allowQuit(false)
		slog.Warn("重启更新客户端失败", "version", u.ready, "error", err)
		return appservice.FailedError(meta, i18n.ErrorClientUpdateFailed)
	}
	return nil
}

// serverProvider 按 Wails 更新清单协议读取当前连接服务器的更新清单，只接受带签名的更新包。
type serverProvider struct {
	serverURL func(context.Context) (string, error)
	mu        sync.Mutex
	// current 是最近一次检查使用的清单读取方，下载沿用同一个。
	current *endpoint.Provider
}

// Name 返回更新来源名称。
func (p *serverProvider) Name() string {
	return "server"
}

// Check 读取当前服务器的更新清单；未连接服务器时视为没有更新。
func (p *serverProvider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	serverURL, err := p.serverURL(ctx)
	if err != nil {
		return nil, err
	}
	if serverURL == "" {
		return nil, nil
	}
	provider, err := endpoint.New(endpoint.Config{URL: strings.TrimRight(serverURL, "/") + clientrelease.UpdatePath})
	if err != nil {
		return nil, err
	}
	release, err := provider.Check(ctx, request)
	if err != nil || release == nil {
		return nil, err
	}
	if release.Verification == nil || len(release.Verification.Signature) == 0 {
		return nil, fmt.Errorf("更新包 %s 缺少签名", release.Artifact.Filename)
	}
	p.mu.Lock()
	p.current = provider
	p.mu.Unlock()
	return release, nil
}

// Download 用最近一次检查的清单读取方下载更新包。
func (p *serverProvider) Download(ctx context.Context, release *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	p.mu.Lock()
	provider := p.current
	p.mu.Unlock()
	if provider == nil {
		return errors.New("尚未检查更新")
	}
	return provider.Download(ctx, release, dst, onProgress)
}
