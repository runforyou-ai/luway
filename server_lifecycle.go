//go:build server

package main

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
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

// telemetryLifecycle 将向 control 上报运行指标接入 Wails 服务生命周期。
type telemetryLifecycle struct {
	db      *bun.DB
	control *control.Client
	metrics *control.Metrics
}

// ServiceStartup 开始每分钟采集并上报运行指标，部署关闭上报时不上报。
func (l *telemetryLifecycle) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	metrics, err := l.control.StartMetrics(ctx, deploymentaction.TelemetryGauges, func(ctx context.Context) (map[string]int64, error) {
		return deploymentaction.TelemetryMetrics(ctx, l.db)
	})
	if err != nil {
		return err
	}
	l.metrics = metrics
	return nil
}

// ServiceShutdown 上报剩余指标后停止采集。
func (l *telemetryLifecycle) ServiceShutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return l.metrics.Shutdown(ctx)
}
