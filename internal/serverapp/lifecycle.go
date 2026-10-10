//go:build server

package serverapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	channelconnectionaction "github.com/runforyou-ai/luway/internal/actions/channelconnection"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/integration/control"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/clusterbus"
	"github.com/uptrace/bun"
)

// component 是随服务端启动、退出时按逆序停止的组件。
type component interface {
	// Start 启动组件，ctx 在服务端退出时取消。
	Start(ctx context.Context) error
	// Stop 停止组件并等待其退出。
	Stop() error
}

// runComponents 按顺序启动组件并等待 ctx 结束，再按逆序停止已启动的组件；某个组件启动失败时停止已启动的组件并返回该错误。
func runComponents(ctx context.Context, components []component) error {
	started := make([]component, 0, len(components))
	var err error
	for _, item := range components {
		if err = item.Start(ctx); err != nil {
			break
		}
		started = append(started, item)
	}
	if err == nil {
		<-ctx.Done()
	}
	for index := len(started) - 1; index >= 0; index-- {
		if stopErr := started[index].Stop(); stopErr != nil {
			slog.WarnContext(context.Background(), "停止服务端组件失败", "error", stopErr)
		}
	}
	return err
}

// serverTaskLifecycle 将服务端任务运行时作为服务端组件。
type serverTaskLifecycle struct {
	runtime *servertask.Runtime
}

// Start 在企业服务端启动后运行异步任务和定时计划。
func (l *serverTaskLifecycle) Start(ctx context.Context) error {
	return l.runtime.Start(ctx)
}

// Stop 停止服务端异步任务运行时。
func (l *serverTaskLifecycle) Stop() error {
	l.runtime.Stop()
	return nil
}

// realtimeLifecycle 将消息总线、实时通知发布器与成员实时网关作为服务端组件，排在全部会写入实时通知的组件之前启动、之后停止。
type realtimeLifecycle struct {
	bus       *clusterbus.Bus
	publisher *realtime.Publisher
	members   *members.Service
}

// Start 加入消息总线，开始发布已提交通知并接收实时事件流请求。
func (l *realtimeLifecycle) Start(ctx context.Context) error {
	if err := l.bus.Start(); err != nil {
		return err
	}
	if err := l.members.Start(ctx); err != nil {
		_ = l.bus.Close()
		return err
	}
	if err := l.publisher.Start(); err != nil {
		_ = l.members.Stop()
		_ = l.bus.Close()
		return err
	}
	return nil
}

// Stop 结束实时事件流、停止实时通知发布器并离开消息总线。
func (l *realtimeLifecycle) Stop() error {
	return errors.Join(l.publisher.Stop(), l.members.Stop(), l.bus.Close())
}

// telemetryLifecycle 将向 control 上报本进程错误作为服务端组件，上报开关取自部署状态，事件带本实例编号。
type telemetryLifecycle struct {
	instanceID string
	deployment *deploymentaction.DeploymentState
	control    *control.Client
	errors     *control.ErrorReporter
}

// Start 把 Error 级别日志接入错误上报；关闭上报时不上报。
func (l *telemetryLifecycle) Start(context.Context) error {
	errors, err := l.control.NewErrorReporter(l.instanceID, l.deployment.TelemetryEnabled)
	if err != nil {
		return err
	}
	l.errors = errors
	slog.SetDefault(slog.New(errors.Handler(slog.Default().Handler())))
	return nil
}

// Stop 等待已上报的错误发送完成。
func (l *telemetryLifecycle) Stop() error {
	l.errors.Flush(5 * time.Second)
	return nil
}

// channelConnectionLifecycle 将渠道长连接运行时作为服务端组件，在任务运行时与实时通知启动后启动。
type channelConnectionLifecycle struct {
	runtime *channelconnectionaction.Runtime
}

// Start 启动渠道长连接的租约核对循环。
func (l *channelConnectionLifecycle) Start(ctx context.Context) error {
	l.runtime.Start(ctx)
	return nil
}

// Stop 关闭本实例持有的渠道长连接并释放租约。
func (l *channelConnectionLifecycle) Stop() error {
	l.runtime.Stop()
	return nil
}

// serverInstance 是本进程登记所在的数据库、加入的消息总线、在部署中的登记信息、运行期间持有的实例锁，以及使本进程退出的原因。
type serverInstance struct {
	db      *bun.DB
	bus     *clusterbus.Bus
	jobs    *jobsNATS
	report  serverinstanceaction.InstanceReport
	lease   *serverinstanceaction.InstanceLease
	exitErr atomic.Pointer[error]
}

// serverInstanceLifecycle 将服务端进程心跳作为服务端组件，在任务运行时与实时通知启动后开始刷新；每次心跳刷新部署状态，实例锁丢失或部署版本与本进程不同时使服务端退出。
type serverInstanceLifecycle struct {
	instance   *serverInstance
	deployment *deploymentaction.DeploymentState
	// interval 是心跳间隔，quit 在本进程需要退出时使服务端退出。
	interval time.Duration
	quit     func()
	cancel   context.CancelFunc
	done     chan struct{}

	// busStats 与 reportedAt 是上一次心跳时消息总线的累计计数与本机时刻，用于计算心跳间隔内的发送速率与失败数。
	busStats   clusterbus.Stats
	reportedAt time.Time
}

// Start 刷新本进程心跳，之后按间隔确认实例锁、刷新心跳与消息总线状态、隔离失联的服务端进程，并刷新部署状态、检查部署版本。
func (l *serverInstanceLifecycle) Start(ctx context.Context) error {
	if err := l.instance.lease.Check(ctx); err != nil {
		return err
	}
	if err := l.heartbeat(ctx); err != nil {
		return err
	}
	ctx, l.cancel = context.WithCancel(context.WithoutCancel(ctx))
	l.done = make(chan struct{})
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(l.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 实例锁丢失后其他进程可能已把本进程视为已退出，停止心跳并使服务端退出，退出流程会停止本循环。
				if err := l.instance.lease.Check(ctx); err != nil {
					if ctx.Err() == nil {
						slog.WarnContext(ctx, "实例锁连接已断开，服务端退出", "error", err)
						l.exit(err)
					}
					return
				}
				if err := l.heartbeat(ctx); err != nil {
					if ctx.Err() == nil {
						slog.WarnContext(ctx, "刷新服务端心跳失败", "error", err)
					}
					continue
				}
				if err := l.deployment.Reload(ctx); err != nil {
					if ctx.Err() == nil {
						slog.WarnContext(ctx, "刷新部署状态失败", "error", err)
					}
					continue
				}
				version := l.deployment.Current().Version
				// 部署版本被其他服务端改写后使服务端退出。
				if buildinfo.CompareVersions(version, l.instance.report.Version) != 0 {
					slog.WarnContext(ctx, "部署版本已改变，服务端退出", "version", l.instance.report.Version, "deployment_version", version)
					l.exit(fmt.Errorf("deployment now runs server version %s, this server runs %s", version, l.instance.report.Version))
					return
				}
			}
		}
	}()
	return nil
}

// Stop 停止心跳。
func (l *serverInstanceLifecycle) Stop() error {
	if l.cancel == nil {
		return nil
	}
	l.cancel()
	<-l.done
	return nil
}

// exit 记录退出原因并使服务端退出。
func (l *serverInstanceLifecycle) exit(err error) {
	l.instance.exitErr.Store(&err)
	l.quit()
}

// heartbeat 先隔离失联的服务端进程，再写入本进程心跳；心跳写入时删除的过期记录都已先经过隔离。
func (l *serverInstanceLifecycle) heartbeat(ctx context.Context) error {
	if terminated, err := serverinstanceaction.FenceLostInstances(ctx, l.instance.db, l.instance.report.ID); err != nil {
		if ctx.Err() != nil {
			return err
		}
		slog.WarnContext(ctx, "隔离失联服务端进程失败", "error", err)
	} else if terminated > 0 {
		slog.WarnContext(ctx, "已终止失联服务端进程的数据库连接", "connections", terminated)
	}
	return l.report(ctx)
}

// report 写入本进程的心跳、消息总线状态，以及距上一次心跳的消息总线发送速率与失败数。
func (l *serverInstanceLifecycle) report(ctx context.Context) error {
	report := l.instance.report
	report.BusConnected = l.instance.bus.Connected()
	stats := l.instance.bus.Stats()
	now := time.Now() //clock:local
	if !l.reportedAt.IsZero() {
		report.BusMessageRate = float64(stats.Sent-l.busStats.Sent) / now.Sub(l.reportedAt).Seconds()
		report.BusFailures = int(stats.Failed - l.busStats.Failed)
	}
	if err := serverinstanceaction.ReportInstance(ctx, l.instance.db, report); err != nil {
		return err
	}
	l.busStats, l.reportedAt = stats, now
	return nil
}
