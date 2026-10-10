//go:build !server && ((darwin && !ios) || windows)

package native

import (
	"context"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
)

// prepareTimeout 是一次检查并下载新版本的最长时间。
const prepareTimeout = 10 * time.Minute

// selfUpdater 是 Wails 更新器中检查、下载与重启的部分。
type selfUpdater interface {
	Check(context.Context) (*updater.Release, error)
	DownloadAndInstall(context.Context) error
	Restart(context.Context) error
}

// clientUpdater 经 Wails 更新器从当前连接的服务器下载、校验并安装桌面端新版本。
type clientUpdater struct {
	updater selfUpdater
	mu      sync.Mutex
	// ready 是已下载并通过签名校验的版本。
	ready string
	// replaceable 表示当前用户能在应用所在目录替换应用。
	replaceable bool
	// server 是最近一次检查更新使用的服务器地址。
	server atomic.Value
	// version 是本机客户端版本。
	version string
}

// NewClientUpdater 创建桌面端更新能力；未注入版本号的开发构建不更新。
func NewClientUpdater(app *application.App) ClientUpdater {
	if buildinfo.Version == "dev" {
		return nil
	}
	client := &clientUpdater{updater: app.Updater, replaceable: applicationReplaceable(), version: buildinfo.Version}
	if err := app.Updater.Init(updaterConfig(buildinfo.Version, brand.Build().UpdateKey(), client.serverURL)); err != nil {
		slog.WarnContext(context.Background(), "初始化客户端更新失败", "error", err)
		return nil
	}
	return client
}

// serverURL 返回最近一次检查更新使用的服务器地址。
func (u *clientUpdater) serverURL(context.Context) (string, error) {
	address, _ := u.server.Load().(string)
	return address, nil
}

// applicationReplaceable 判断当前用户能否在应用所在目录替换应用：macOS 为应用包所在目录，Windows 为可执行文件所在目录。
func applicationReplaceable() bool {
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	target := executable
	// macOS 替换整个应用包。
	for dir := filepath.Dir(executable); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if strings.HasSuffix(dir, ".app") {
			target = dir
			break
		}
	}
	probe, err := os.CreateTemp(filepath.Dir(target), ".update-probe-*")
	if err != nil {
		return false
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return true
}

// updaterConfig 返回从当前服务器读取更新清单、用品牌公钥校验更新包签名且不打开更新窗口的更新器配置。
func updaterConfig(version string, publicKey ed25519.PublicKey, serverURL func(context.Context) (string, error)) updater.Config {
	return updater.Config{
		CurrentVersion: version,
		Providers:      []updater.Provider{&serverProvider{serverURL: serverURL, publicKey: publicKey}},
		Window:         updater.WindowNone,
	}
}

// PrepareClientUpdate 检查指定服务器提供的客户端版本，较新时下载更新包并校验签名，同一版本只下载一次；服务器没有本机平台的更新包或当前用户不能替换应用时不下载，返回 available。超过 prepareTimeout 按失败返回。
func (u *clientUpdater) PrepareClientUpdate(ctx context.Context, meta appservice.RequestMeta, serverURL string) (ClientUpdate, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, prepareTimeout)
	defer cancel()
	u.server.Store(serverURL)
	offered, err := readOfferedUpdate(ctx, serverURL, u.version)
	if err != nil {
		slog.WarnContext(ctx, "检查客户端更新失败", "error", err)
		return ClientUpdate{}, appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, err)
	}
	if !offered.NewerThan(u.version) {
		u.ready = ""
		return ClientUpdate{State: ClientUpdateStateCurrent}, nil
	}
	if !offered.Installable || !u.replaceable {
		return ClientUpdate{State: ClientUpdateStateAvailable, Version: offered.Version}, nil
	}
	release, err := u.updater.Check(ctx)
	if err != nil {
		slog.WarnContext(ctx, "检查客户端更新失败", "error", err)
		return ClientUpdate{}, appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, err)
	}
	if release == nil {
		u.ready = ""
		return ClientUpdate{State: ClientUpdateStateCurrent}, nil
	}
	if release.Version != u.ready {
		u.ready = ""
		if err := u.updater.DownloadAndInstall(ctx); err != nil {
			slog.WarnContext(ctx, "下载客户端更新失败", "version", release.Version, "error", err)
			return ClientUpdate{}, appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, err)
		}
		u.ready = release.Version
		slog.InfoContext(ctx, "客户端新版本已就绪", "version", release.Version)
	}
	return ClientUpdate{State: ClientUpdateStateReady, Version: u.ready}, nil
}

// RestartClientUpdate 交给 Wails 更新器替换程序并重新启动。
func (u *clientUpdater) RestartClientUpdate(ctx context.Context, meta appservice.RequestMeta) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ready == "" {
		return appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, errors.New("没有已准备好的新版本"))
	}
	if err := u.updater.Restart(ctx); err != nil {
		slog.WarnContext(ctx, "重启更新客户端失败", "version", u.ready, "error", err)
		return appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, err)
	}
	return nil
}

// serverProvider 按 Wails 更新清单协议读取当前连接服务器的更新清单，只接受签名覆盖清单版本与摘要的更新包，更新器下载后按该摘要校验内容。
type serverProvider struct {
	serverURL func(context.Context) (string, error)
	publicKey ed25519.PublicKey
	mu        sync.Mutex
	// current 是最近一次检查使用的清单读取方，下载沿用同一个。
	current *endpoint.Provider
}

// Name 返回更新来源名称。
func (p *serverProvider) Name() string {
	return "server"
}

// Check 读取当前服务器的更新清单并校验更新包签名；未连接服务器时视为没有更新。
func (p *serverProvider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	serverURL, err := p.serverURL(ctx)
	if err != nil {
		return nil, err
	}
	if serverURL == "" {
		return nil, nil
	}
	// 下载总时长由 PrepareClientUpdate 的截止时间限制，服务器迟迟不返回响应头时直接失败。
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	client := &http.Client{Transport: transport}
	provider, err := endpoint.New(endpoint.Config{URL: strings.TrimRight(serverURL, "/") + clientrelease.UpdatePath, HTTPClient: client})
	if err != nil {
		return nil, err
	}
	release, err := provider.Check(ctx, request)
	if err != nil || release == nil {
		return nil, err
	}
	encoded, _ := release.Metadata[clientrelease.SignatureMetadataKey].(string)
	signature, _ := base64.StdEncoding.DecodeString(encoded)
	verification := release.Verification
	if verification == nil || verification.DigestAlgo != "sha512" || len(verification.Digest) != sha512.Size ||
		!ed25519.Verify(p.publicKey, clientrelease.UpdateStatement(release.Version, verification.Digest), signature) {
		return nil, fmt.Errorf("更新包 %s 的签名无效", release.Artifact.Filename)
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
