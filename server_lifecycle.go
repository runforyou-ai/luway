//go:build server

package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/ingress"
	"github.com/runforyou-ai/luway/internal/integration/control"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/gateway"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// httpsLifecycle 将 HTTPS 入口接入 Wails 服务生命周期。
type httpsLifecycle struct {
	service *ingress.HTTPSEntry
}

// ServiceStartup 启动 HTTPS 入口。
func (l *httpsLifecycle) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	return l.service.Start(ctx)
}

// ServiceShutdown 关闭 HTTPS 入口。
func (l *httpsLifecycle) ServiceShutdown() error {
	return l.service.Shutdown()
}

// serverTaskLifecycle 将服务端任务运行时接入 Wails 服务生命周期。
type serverTaskLifecycle struct {
	runtime *servertask.Runtime
}

// ServiceStartup 在企业服务端启动后运行异步任务和定时计划。
func (l *serverTaskLifecycle) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	return l.runtime.Start(ctx)
}

// ServiceShutdown 停止服务端异步任务和 NATS 连接。
func (l *serverTaskLifecycle) ServiceShutdown() error {
	return l.runtime.Stop()
}

// realtimeLifecycle 将实时通知发布器与成员实时网关接入 Wails 服务生命周期。
type realtimeLifecycle struct {
	publisher *realtime.Publisher
	gateway   *gateway.Gateway
}

// ServiceStartup 连接 NATS，开始发布已提交通知并接收实时事件流请求。
func (l *realtimeLifecycle) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	if err := l.publisher.Start(); err != nil {
		return err
	}
	l.gateway.Start(l.publisher.Connection())
	// 收到 SIGINT、SIGTERM 时立即结束实时事件流。
	signals, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals.Done()
		stop()
		l.gateway.Shutdown()
	}()
	return nil
}

// ServiceShutdown 结束实时事件流后停止实时通知发布器。
func (l *realtimeLifecycle) ServiceShutdown() error {
	l.gateway.Shutdown()
	return l.publisher.Stop()
}

// telemetryLifecycle 将向 control 上报运行指标与错误接入 Wails 服务生命周期。
type telemetryLifecycle struct {
	telemetry *platformaction.Telemetry
	control   *control.Client
	metrics   *control.Metrics
	errors    *control.ErrorReporter
}

// ServiceStartup 读取上报开关并定期刷新，把 Error 级别日志接入错误上报，开始每分钟采集并上报运行指标；关闭上报时不上报。
func (l *telemetryLifecycle) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	if err := l.telemetry.Refresh(ctx); err != nil {
		return err
	}
	go l.telemetry.Run(ctx)
	errors, err := l.control.NewErrorReporter(l.telemetry.Enabled)
	if err != nil {
		return err
	}
	l.errors = errors
	slog.SetDefault(slog.New(errors.Handler(slog.Default().Handler())))
	metrics, err := l.control.StartMetrics(ctx, platformaction.TelemetryGauges, l.telemetry.Metrics)
	if err != nil {
		return err
	}
	l.metrics = metrics
	return nil
}

// ServiceShutdown 上报剩余指标后停止采集，并等待已上报的错误发送完成。
func (l *telemetryLifecycle) ServiceShutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := l.metrics.Shutdown(ctx)
	l.errors.Flush(5 * time.Second)
	return err
}

// serverInstanceLifecycle 将服务端进程心跳接入 Wails 服务生命周期，在任务运行时与实时通知启动后注册；config 是登记时写入的服务端配置。
type serverInstanceLifecycle struct {
	db        *bun.DB
	tasks     *servertask.Runtime
	publisher *realtime.Publisher
	hostname  string
	config    serverconfig.Diagnostics
	cancel    context.CancelFunc
	done      chan struct{}
}

// ServiceStartup 登记服务端进程，之后按间隔刷新心跳与 NATS 连接状态。
func (l *serverInstanceLifecycle) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	if err := l.report(ctx); err != nil {
		return err
	}
	ctx, l.cancel = context.WithCancel(context.WithoutCancel(ctx))
	l.done = make(chan struct{})
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(platformaction.InstanceHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := l.report(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("刷新服务端心跳失败", "error", err)
				}
			}
		}
	}()
	return nil
}

// ServiceShutdown 停止心跳并删除本进程记录。
func (l *serverInstanceLifecycle) ServiceShutdown() error {
	if l.cancel == nil {
		return nil
	}
	l.cancel()
	<-l.done
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return platformaction.RemoveInstance(ctx, l.db, l.tasks.InstanceID())
}

// report 写入本进程的心跳、后台任务与实时通知各自的 NATS 连接状态，以及登记时的服务端配置。
func (l *serverInstanceLifecycle) report(ctx context.Context) error {
	connection := l.publisher.Connection()
	return platformaction.ReportInstance(ctx, l.db, platformaction.InstanceReport{
		ID: l.tasks.InstanceID(), Hostname: l.hostname, Version: buildinfo.Version,
		TasksNATSConnected: l.tasks.BrokerConnected(), RealtimeNATSConnected: connection != nil && connection.IsConnected(),
		Config: l.config,
	})
}
