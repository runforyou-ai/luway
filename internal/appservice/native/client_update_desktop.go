//go:build !server && ((darwin && !ios) || windows || (linux && !android))

package native

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
	"golang.org/x/mod/semver"
)

// clientUpdater 比较所连接服务器提供的客户端版本，并经 Wails 更新器从该服务器安装更新。
type clientUpdater struct {
	server    ClientUpdateServer
	updater   *updater.Updater
	allowQuit func(bool)
}

// NewClientUpdater 创建桌面端应用内更新能力；构建品牌配置了更新公钥、版本为语义化版本、平台能替换运行中的应用（macOS、Windows）且当前用户可写应用所在目录时启用应用内安装。allowQuit 在重启完成更新前放行应用退出，重启失败时撤销。
func NewClientUpdater(server ClientUpdateServer, allowQuit func(bool)) appservice.ClientUpdater {
	syncInstalledVersion()
	client := &clientUpdater{server: server, allowQuit: allowQuit}
	key := brand.Build().UpdateKey()
	if key == nil || !semver.IsValid("v"+buildinfo.Version) || runtime.GOOS == "linux" || !applicationReplaceable() {
		return client
	}
	app := application.Get()
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: buildinfo.Version,
		Providers:      []updater.Provider{serverProvider{server: server}},
		PublicKey:      key,
		Window:         updater.WindowNone,
	}); err != nil {
		slog.Warn("初始化应用内更新失败", "error", err)
		return client
	}
	client.updater = app.Updater
	return client
}

// CheckClientUpdate 返回本机版本，以及所连接服务器提供的客户端版本较新时的该版本。
func (c *clientUpdater) CheckClientUpdate(ctx context.Context, meta appservice.RequestMeta) (appservice.ClientUpdate, error) {
	result := appservice.ClientUpdate{CurrentVersion: buildinfo.Version}
	status, err := c.server.InstallationStatus(ctx, meta)
	if err != nil {
		return appservice.ClientUpdate{}, err
	}
	current, offered := "v"+buildinfo.Version, "v"+status.ClientVersion
	if semver.IsValid(current) && semver.IsValid(offered) && semver.Compare(offered, current) > 0 {
		result.Version = status.ClientVersion
		result.Installable = c.updater != nil
	}
	return result, nil
}

// InstallClientUpdate 下载并验证所连接服务器提供的更新包，替换应用后退出并由更新助手重新启动。
func (c *clientUpdater) InstallClientUpdate(ctx context.Context, meta appservice.RequestMeta) error {
	if c.updater == nil {
		return appservice.FailedError(meta, i18n.ErrorMethodNotAllowed, nil)
	}
	release, err := c.updater.Check(ctx)
	if err != nil {
		slog.Warn("检查客户端更新失败", "error", err)
		return appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, err)
	}
	if release == nil {
		return appservice.FailedError(meta, i18n.ErrorClientUpdateUnavailable, nil)
	}
	if err := c.updater.DownloadAndInstall(ctx); err != nil {
		slog.Warn("下载或验证客户端更新失败", "version", release.Version, "error", err)
		return appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, err)
	}
	slog.Info("客户端更新已就绪，重启应用", "version", release.Version)
	c.allowQuit(true)
	if err := c.updater.Restart(ctx); err != nil {
		c.allowQuit(false)
		slog.Warn("重启以完成客户端更新失败", "version", release.Version, "error", err)
		return appservice.FailedError(meta, i18n.ErrorClientUpdateFailed, err)
	}
	return nil
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
	probe.Close()
	_ = os.Remove(probe.Name())
	return true
}

// updateHTTPClient 只限制建立连接与等待响应头的时间，更新包下载时长不设上限，随安装调用的 context 取消。
var updateHTTPClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   30 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}}

// serverProvider 每次检查时按当前保存的服务器地址请求该服务器的更新清单。
type serverProvider struct {
	server ClientUpdateServer
}

// Name 返回更新来源名称。
func (serverProvider) Name() string { return "server" }

// endpoint 返回指向当前服务器更新清单的 Wails 更新清单来源。
func (p serverProvider) endpoint(ctx context.Context) (*endpoint.Provider, error) {
	serverURL, err := p.server.ServerURL(ctx, appservice.RequestMeta{})
	if err != nil {
		return nil, err
	}
	if serverURL == "" {
		return nil, errors.New("尚未连接服务器")
	}
	return endpoint.New(endpoint.Config{URL: serverURL + domain.ClientUpdatePath, HTTPClient: updateHTTPClient})
}

// Check 查询当前服务器为本机平台提供的较新更新包，更新包必须带 Ed25519ph 签名。
func (p serverProvider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	provider, err := p.endpoint(ctx)
	if err != nil {
		return nil, err
	}
	release, err := provider.Check(ctx, request)
	if err != nil || release == nil {
		return release, err
	}
	// 更新器对不带签名的更新包只校验摘要，这里拒绝未签名的更新包。
	if release.Verification == nil || release.Verification.SignatureAlgo != "ed25519ph" || len(release.Verification.Signature) == 0 {
		return nil, errors.New("更新包未按 Ed25519ph 签名")
	}
	return release, nil
}

// Download 从当前服务器下载检查得到的更新包。
func (p serverProvider) Download(ctx context.Context, release *updater.Release, destination io.Writer, onProgress func(written, total int64)) error {
	provider, err := p.endpoint(ctx)
	if err != nil {
		return err
	}
	return provider.Download(ctx, release, destination, onProgress)
}
