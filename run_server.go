//go:build server

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/storage"
	"github.com/runforyou-ai/luway/internal/webasset"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// run 解析服务端运行参数并启动 HTTP 服务。
func run(arguments []string) error {
	flags := flag.NewFlagSet(brand.Build().Slug+"-server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "显式指定 YAML 配置文件")
	checkConfig := flags.Bool("check-config", false, "校验配置后退出")
	showVersion := flags.Bool("version", false, "输出版本号后退出")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse server arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional argument %q", flags.Arg(0))
	}
	if *showVersion {
		_, err := fmt.Fprintln(os.Stdout, buildinfo.Version)
		return err
	}

	config, err := serverconfig.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load server config: %w", err)
	}
	if *checkConfig {
		_, err := fmt.Fprintln(os.Stdout, "服务端配置有效")
		return err
	}
	if err := brand.Configure(config.Branding.Override()); err != nil {
		return fmt.Errorf("configure branding: %w", err)
	}

	appStorage, err := storage.Open(context.Background(), config.Database)
	if err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	defer func() {
		if err := appStorage.Close(); err != nil {
			slog.Warn("关闭存储失败", "error", err)
		}
	}()

	services, realtimeMiddleware, err := applicationServices(appStorage, config)
	if err != nil {
		return fmt.Errorf("initialize application services: %w", err)
	}

	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return fmt.Errorf("open frontend assets: %w", err)
	}
	assetServer, err := webasset.NewFileServer(dist, "assets")
	if err != nil {
		return err
	}
	if config.Branding.IconPath != "" {
		icon, err := os.ReadFile(config.Branding.IconPath)
		if err != nil {
			return fmt.Errorf("read branding icon: %w", err)
		}
		assetServer.Replace("favicon.png", icon)
	}

	app := application.New(application.Options{
		Name:        brand.Current().DisplayName(),
		Description: brand.Current().Description,
		Services:    services,
		// 由 Wails 服务端运行时监听退出信号。
		DisableDefaultSignalHandler: true,
		Assets: application.AssetOptions{
			Handler: assetServer,
			// 实时事件流在 Wails 资源服务之前处理；托管部署另外接收官方账号登录回调。
			Middleware: func(next http.Handler) http.Handler {
				handler := realtimeMiddleware(next)
				if config.Deployment.Mode.Managed() {
					handler = api.OfficialLoginCallbackMiddleware(handler)
				}
				return handler
			},
		},
		Server: application.ServerOptions{
			Host: config.Server.Host,
			Port: config.Server.Port,
		},
	})

	slog.Info("启动服务端", "version", buildinfo.Version, "host", config.Server.Host, "port", config.Server.Port, "tls_mode", config.TLS.Mode)
	runErr := app.Run()
	// Run 返回后同步执行应用清理。
	app.Quit()
	if runErr == nil {
		slog.Info("服务端已停止")
	}
	return runErr
}
