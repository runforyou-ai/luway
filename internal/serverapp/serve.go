//go:build server

// Package serverapp 组装服务端程序：加入部署、创建依赖、注册业务服务与后台任务，并运行 HTTP 服务。
package serverapp

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"uuid"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	serverlogaction "github.com/runforyou-ai/luway/internal/actions/serverlog"
	workspaceaction "github.com/runforyou-ai/luway/internal/actions/workspace"
	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/productsite"
	"github.com/runforyou-ai/luway/internal/servercli"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/internal/webasset"
	"github.com/runforyou-ai/luway/pkg/clusterbus"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/uptrace/bun"
)

// Edition 定义服务端程序的组成：程序的数据库迁移、/app/ 下提供的 Web 前端构建产物，以及服务装配时挂接的其他模块内容。
type Edition struct {
	Migrations serverstorage.Migrations
	WebAssets  fs.FS
	// Extend 在数据库、任务运行时与邮件发送创建后以本进程依赖创建挂接内容，每个进程只调用一次；为空时只有核心功能。调用时任务尚未注册，挂接方只保存投递器并声明任务，不在其中投递。
	Extend func(Host) (Extension, error)
}

// Host 是程序组成创建挂接内容时可用的本进程依赖。
type Host struct {
	DB *bun.DB
	// Config 是本进程的启动配置。
	Config serverconfig.Config
	// Tasks 立即或随业务事务投递后台任务。
	Tasks servertask.DualEnqueuer
	// Mail 按部署配置发送邮件。
	Mail Mailer
	// PublicURL 返回当前部署地址。
	PublicURL func() string
}

// Mailer 按部署配置发送邮件，部署未配置邮件发送时 Enabled 为 false。
type Mailer interface {
	Enabled() bool
	Send(ctx context.Context, message mail.Message) error
}

// Extension 是程序组成在服务装配时挂接的内容，零值表示不挂接。
type Extension struct {
	// WorkspaceInitializer 在工作区创建事务内初始化附加数据。
	WorkspaceInitializer workspaceaction.Initializer
	// ModelCallLifecycle 与模型调用记录共用开始和结束事务。
	ModelCallLifecycle modelcall.Lifecycle
	// APIRoutes 注册挂到 /api 下的其他模块路由。
	APIRoutes func(handle func(string, http.HandlerFunc))
	// SeatLimit 是工作区的席位上限来源，为空时不限席位。
	SeatLimit seataction.Limit
	// ProductDocs 是追加到基础导航中的产品文档来源，为空时只加载基础来源。
	ProductDocs fs.FS
	// HomePricing 读取产品首页价格区块的内容，为空时首页不展示价格区块。
	HomePricing func(context.Context) (productsite.Pricing, error)
	// Tasks 是其他模块的后台任务，在核心任务之后注册。
	Tasks []servertask.Action
	// Schedules 是其他模块的定时计划，核心在注册 Tasks 后登记；引用的 Action 未注册时任务运行时启动失败。
	Schedules []servertask.ScheduleDefinition
}

// Program 返回该组成的服务端程序，供 servercli 执行子命令。
func (e Edition) Program() servercli.Program {
	return servercli.Program{Migrations: e.Migrations, Serve: e.Serve}
}

// Serve 按启动配置加入部署并运行 HTTP 服务，ctx 取消、收到 SIGINT 或 SIGTERM，或服务端自行退出时返回。
func (e Edition) Serve(ctx context.Context, config serverconfig.Config) error {
	ctx, stopSignals := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	// 本进程的全部数据库连接以实例编号命名，失联时由其他服务端进程按名称终止。
	instanceID := uuid.NewV7().String()
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("read hostname: %w", err)
	}
	// 全部日志从此处起排队写入服务端日志，加入部署后开始写入；未开始写入就返回时丢弃排队的日志。
	logs := serverlogaction.NewServerLog(instanceID, hostname, buildinfo.Version)
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(logs.Handler(previousLogger.Handler())))
	defer func() {
		logs.Stop()
		slog.SetDefault(previousLogger)
	}()
	appStorage, err := serverstorage.Connect(ctx, config.Database, e.Migrations, serverinstanceaction.InstanceApplicationName(instanceID), servertask.WorkerCount())
	if err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	defer func() {
		if err := appStorage.Close(); err != nil {
			slog.WarnContext(ctx, "关闭存储失败", "error", err)
		}
	}()
	programDirectory, err := clientrelease.ProgramDirectory()
	if err != nil {
		return err
	}
	filesDirectory := config.Data.FilesDirectory()

	// 本进程加入部署：版本与部署一致后执行迁移并登记，退出时删除登记并释放实例锁；配置 NATS 地址时消息总线经 NATS 传输，否则经 PostgreSQL 传输。
	var bus *clusterbus.Bus
	if config.NATS.URL != "" {
		bus = clusterbus.NewNATS(instanceID, clusterbus.NATSOptions{URL: config.NATS.URL, Namespace: config.NATS.Namespace})
	} else {
		bus = clusterbus.NewPostgres(instanceID, appStorage.DB().DB, appStorage.ConnConfig(), clusterbus.PostgresOptions{Channel: "bus"})
	}
	instance := &serverInstance{db: appStorage.DB(), bus: bus, report: serverinstanceaction.InstanceReport{
		ID: instanceID, Hostname: hostname, Version: buildinfo.Version, BusDriver: bus.Driver(),
		Config: config.Diagnostics(filepath.Join(programDirectory, clientrelease.DirName)),
	}}
	instance.lease, err = serverinstanceaction.AdmitInstance(ctx, appStorage.DB(), serverinstanceaction.Admission{
		Instance: instance.report, Migrate: appStorage.Migrate,
	})
	if err != nil {
		return fmt.Errorf("join deployment: %w", err)
	}
	if err := logs.Start(ctx, appStorage.DB()); err != nil {
		return fmt.Errorf("start server logs: %w", err)
	}
	// 关闭存储前写完已排队的日志。
	defer logs.Stop()
	// 任务队列经 NATS JetStream 传输：未配置 NATS 地址时在本进程内启动 NATS，只用于单台服务器。
	instance.jobs, err = connectJobsNATS(ctx, appStorage.DB(), config, instanceID, hostname)
	if err != nil {
		return fmt.Errorf("connect task queue: %w", err)
	}
	defer instance.jobs.Close()
	defer func() {
		defer instance.lease.Release()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := serverinstanceaction.RemoveInstance(ctx, appStorage.DB(), instance.report.ID); err != nil {
			slog.WarnContext(ctx, "删除服务端进程登记失败", "error", err)
		}
	}()

	// 读取部署配置、上报开关与授权，部署品牌只在授权授予自定义品牌且未到期时生效。
	deployment := deploymentaction.NewDeploymentState(appStorage.DB())
	if err := deployment.Reload(ctx); err != nil {
		return fmt.Errorf("load deployment state: %w", err)
	}
	brand.UseOverride(deployment.BrandOverride)
	if current := deployment.Current(); current.Installed && current.Settings.PublicURL == "" {
		slog.WarnContext(ctx, "部署地址为空，服务端生成的链接不可用，请在平台设置的部署配置中填写部署地址")
	}
	if branding := deployment.Current().Settings.Branding; (len(branding.Names) > 0 || branding.SDKName != "" || len(branding.Icon) > 0) && !brand.OverrideActive() {
		slog.WarnContext(ctx, "授权未授予自定义品牌或授权已到期，部署品牌暂不生效")
	}

	clients, err := clientrelease.Load(programDirectory, buildinfo.Version, brand.Build().UpdateKey())
	if err != nil {
		return fmt.Errorf("load client downloads: %w", err)
	}
	assetServer, err := webasset.NewFileServer(e.WebAssets, "assets")
	if err != nil {
		return err
	}
	// 部署品牌生效且配置了网站图标时替换网站图标。
	assetServer.Override("favicon.png", func() (string, []byte) {
		if !brand.OverrideActive() {
			return "", nil
		}
		current := deployment.Current()
		return current.IconDigest, current.Settings.Branding.Icon
	})

	// Web 应用位于 /app/ 下，客户端安装包位于 /clients/ 下，网站图标保留在根路径，/index.html 跳转到应用，其余路径由产品站处理。
	routes := http.NewServeMux()
	routes.Handle(domain.WebAppPath, http.StripPrefix(strings.TrimSuffix(domain.WebAppPath, "/"), assetServer))
	routes.Handle(clientrelease.PathPrefix, clients)
	routes.Handle("/index.html", http.RedirectHandler(domain.WebAppPath, http.StatusMovedPermanently))
	routes.Handle("/favicon.png", assetServer)

	// 进程心跳要求退出时取消 ctx，与收到退出信号同样停止全部组件。
	ctx, quit := context.WithCancel(ctx)
	defer quit()
	components, err := applicationServices(ctx, e.Extend, appStorage, config, routes, clients, filesDirectory, instance, deployment, quit)
	if err != nil {
		return fmt.Errorf("initialize application services: %w", err)
	}

	slog.InfoContext(ctx, "启动服务端", "version", buildinfo.Version, "host", config.Server.Host, "port", config.Server.Port, "https_port", config.Server.HTTPSPort)
	if err := runComponents(ctx, components); err != nil {
		return err
	}
	if exitErr := instance.exitErr.Load(); exitErr != nil {
		return *exitErr
	}
	slog.InfoContext(context.WithoutCancel(ctx), "服务端已停止")
	return nil
}

// productHome 返回产品首页随部署状态变化的内容：程序组成提供的价格区块、自部署介绍的部署配置与注册策略。
func productHome(deployment *deploymentaction.DeploymentState, pricing func(context.Context) (productsite.Pricing, error)) func(context.Context) (productsite.Home, error) {
	return func(ctx context.Context) (productsite.Home, error) {
		current := deployment.Current()
		home := productsite.Home{
			SelfHost:         current.Settings.HomeSelfHost,
			RegistrationOpen: current.RegistrationPolicy == domain.RegistrationPolicyOpen,
		}
		if pricing == nil {
			return home, nil
		}
		listed, err := pricing(ctx)
		home.Pricing = &listed
		return home, err
	}
}
