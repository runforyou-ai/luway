package appservice

import (
	"context"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/common/brand"
)

// LoadStartup 根据部署安装状态返回初始化、服务器连接、客户端升级或就绪入口和界面品牌；登录与工作区选择由后续身份加载决定。
func (s *Service) LoadStartup(ctx context.Context, meta RequestMeta) (Startup, error) {
	var startup Startup
	var err error
	if connector, ok := s.backend.(ServerConnector); ok {
		startup, err = s.loadNativeStartup(ctx, meta, connector)
	} else {
		// 部署尚无账号时进入初始化页。
		status, statusErr := s.backend.InstallationStatus(ctx, meta)
		if statusErr != nil {
			return Startup{}, statusErr
		}
		startup = Startup{State: SessionStateReady, Brand: status.Brand}
		if !status.Installed {
			startup.State = SessionStateSetup
		}
	}
	if err != nil {
		return Startup{}, err
	}
	slog.Info("应用启动检测完成", "state", startup.State)
	return startup, nil
}

// loadNativeStartup 检测原生端已保存服务器的连通、安装状态和接口版本；进入连接页或升级页时使用本机构建品牌，进入连接页时说明已保存服务器不可用的原因，就绪后使用服务器下发的品牌。
func (s *Service) loadNativeStartup(ctx context.Context, meta RequestMeta, connector ServerConnector) (Startup, error) {
	build := brand.Build()
	connect := Startup{State: SessionStateConnect, Brand: Brand{Names: build.Names, SDKName: build.SDKName, LinkScheme: build.Slug}}
	serverURL, err := connector.ServerURL(ctx, meta)
	if err != nil {
		slog.Warn("读取服务器地址失败，进入连接页", "error", err)
		return connect, nil
	}
	if serverURL == "" {
		slog.Info("原生端尚未配置服务器，进入连接页")
		return connect, nil
	}
	status, err := s.backend.InstallationStatus(ctx, meta)
	if err != nil {
		slog.Warn("读取服务器安装状态失败，进入连接页", "server_url", serverURL, "error", err)
		connect.ConnectReason = ConnectReasonUnreachable
		return connect, nil
	}
	if !status.Installed {
		slog.Info("服务器尚未完成首次安装，进入连接页", "server_url", serverURL)
		connect.ConnectReason = ConnectReasonNotInstalled
		return connect, nil
	}
	if status.ServerOutdated() {
		slog.Info("服务器接口版本过旧，进入连接页", "server_url", serverURL, "server_api_version", status.APIVersion)
		connect.ConnectReason = ConnectReasonServerOutdated
		return connect, nil
	}
	if status.ClientOutdated() {
		slog.Info("服务器要求升级客户端，进入升级页", "server_url", serverURL, "min_client_api_version", status.MinClientAPIVersion)
		return Startup{State: SessionStateUpgrade, Brand: connect.Brand}, nil
	}
	return Startup{State: SessionStateReady, Brand: status.Brand}, nil
}
