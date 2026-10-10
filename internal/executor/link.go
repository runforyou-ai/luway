//go:build !server && !ios && !android

package executor

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	// pollInterval 是不依赖实时通知、定期领取操作的间隔。
	pollInterval = 30 * time.Second
	// retryInterval 是请求失败后首次重试的间隔，连续失败按倍数退避到 maxRetryInterval，每次间隔随机浮动 20%。
	retryInterval = 5 * time.Second
	// maxRetryInterval 是连续失败后重试间隔翻倍的上限。
	maxRetryInterval = time.Minute
	// defaultConcurrency 是未指定并发上限时同时执行的操作数。
	defaultConcurrency = 4
)

// LinkOptions 定义一条执行器连接：服务器地址、电脑凭据、执行环境与同时执行的操作上限，上限为零时使用默认值，凭据失效时调用 OnCredentialInvalid。
type LinkOptions struct {
	ServerURL           string
	Credential          string
	Host                *Host
	Concurrency         int
	OnCredentialInvalid func()
}

// Link 以电脑身份连接一个工作区：维持 Jetcast 通知与独立 HTTP 心跳，领取派发给这台电脑的操作并上报结果，执行能力变化时重新上报。
type Link struct {
	options LinkOptions
	client  *client
	ctx     context.Context
	cancel  context.CancelFunc
	wake    chan struct{}
	report  chan struct{}
	slots   chan struct{}
	loops   sync.WaitGroup
	revoke  sync.Once
	// unsubscribe 取消对执行能力变化的登记。
	unsubscribe func()
	// agents 是这条连接上进行中的本机 Agent 会话。
	agents *localAgentPool
	// mu 保护 running 与 permissions。
	mu sync.Mutex
	// running 是本机正在执行或尚未上报结果的操作，值为中止该操作的取消函数。
	running map[string]context.CancelFunc
	// permissions 是等待裁决的本机 Agent 权限请求，值接收选用的处理方式编号，为空表示请求已取消。
	permissions map[string]chan string
}

// StartLink 建立执行器连接并开始实时通知、HTTP 心跳、领取与能力上报循环；服务器地址无效时返回错误。
func StartLink(options LinkOptions) (*Link, error) {
	client, err := newClient(options.ServerURL, options.Credential)
	if err != nil {
		return nil, err
	}
	concurrency := options.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	ctx, cancel := context.WithCancel(context.Background())
	link := &Link{
		options: options, client: client, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), report: make(chan struct{}, 1), slots: make(chan struct{}, concurrency),
		running: map[string]context.CancelFunc{}, agents: newLocalAgentPool(), permissions: map[string]chan string{},
	}
	link.unsubscribe = options.Host.Subscribe(func() { signal(link.report) })
	signal(link.report)
	link.loops.Go(link.listen)
	link.loops.Go(link.work)
	link.loops.Go(link.reportLoop)
	link.loops.Go(link.heartbeat)
	signal(link.wake)
	return link, nil
}

// Stop 结束连接、取消能力变化登记，等待执行中的操作退出并关闭本机 Agent 会话。
func (l *Link) Stop() {
	l.cancel()
	l.unsubscribe()
	l.loops.Wait()
	l.agents.close()
}

// heldOperations 返回本机正在执行或尚未上报结果的操作编号。
func (l *Link) heldOperations() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Collect(maps.Keys(l.running))
}

// pendingPermissions 返回等待裁决的权限请求编号。
func (l *Link) pendingPermissions() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Collect(maps.Keys(l.permissions))
}

// rejected 处理服务端拒绝请求的错误：凭据失效或执行器版本与服务端不一致时结束连接并返回 true。
func (l *Link) rejected(err error) bool {
	switch {
	case errors.Is(err, ErrCredentialInvalid):
		l.credentialInvalid()
		return true
	case errors.Is(err, ErrExecutorOutdated):
		l.revoke.Do(func() {
			slog.ErrorContext(context.Background(), "执行器版本与服务端不一致，停止执行器连接，请升级到与服务端一致的版本", "server", l.options.ServerURL, "version", domain.ExecutorVersion)
			l.cancel()
		})
		return true
	}
	return false
}

// credentialInvalid 在凭据失效时结束连接并通知一次。
func (l *Link) credentialInvalid() {
	l.revoke.Do(func() {
		slog.WarnContext(context.Background(), "电脑凭据已失效，停止执行器连接", "server", l.options.ServerURL)
		l.cancel()
		if l.options.OnCredentialInvalid != nil {
			go l.options.OnCredentialInvalid()
		}
	})
}

// newRetryBackOff 返回执行器请求失败后的重试退避：从 retryInterval 起按倍数增长到 maxRetryInterval，间隔随机浮动，不限总时长。
func newRetryBackOff() *backoff.ExponentialBackOff {
	b := &backoff.ExponentialBackOff{InitialInterval: retryInterval, RandomizationFactor: 0.2, Multiplier: 2, MaxInterval: maxRetryInterval}
	b.Reset()
	return b
}

// listen 维持 SDK 工作通知连接并在订阅就绪后领取操作，连接停止后退避重连，凭据失效或版本不一致时结束本工作区连接。
func (l *Link) listen() {
	retry := newRetryBackOff()
	for {
		connected := l.connectRealtime()
		if l.ctx.Err() != nil {
			return
		}
		if connected {
			retry.Reset()
		}
		select {
		case <-l.ctx.Done():
			return
		case <-time.After(retry.NextBackOff()):
		}
	}
}

// work 在通知与定时器上领取操作，按空闲槽位数领取并并发执行，并中止服务端要求中止的操作，直到连接结束。
func (l *Link) work() {
	var running sync.WaitGroup
	defer running.Wait()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-l.wake:
		case <-time.After(pollInterval):
		}
		// 每次领取都带上本机仍持有的操作、本机 Agent 会话与等待裁决的权限请求，服务端据此结算已无人执行的调用并给出要中止的操作、已释放的会话与权限请求的结果；
		// 槽位占满时只核对不领取，委派本机 Agent 的轮次不占槽位。
		claimed, err := l.client.claimComputerOperations(l.ctx, appservice.ComputerClaimInput{
			Limit: cap(l.slots) - len(l.slots), Running: l.heldOperations(), Sessions: l.agents.held(), Permissions: l.pendingPermissions(),
		})
		if l.rejected(err) {
			return
		}
		if err != nil {
			if l.ctx.Err() == nil {
				slog.WarnContext(context.Background(), "领取电脑操作失败", "server", l.options.ServerURL, "error", err)
			}
			continue
		}
		l.mu.Lock()
		aborts := make([]context.CancelFunc, 0, len(claimed.Abort))
		for _, id := range claimed.Abort {
			if abort := l.running[id]; abort != nil {
				slog.InfoContext(context.Background(), "按服务端要求中止电脑操作", "operation_id", id)
				aborts = append(aborts, abort)
			}
		}
		for _, result := range claimed.Permissions {
			if waiting := l.permissions[result.ID]; waiting != nil {
				waiting <- result.OptionID
				delete(l.permissions, result.ID)
			}
		}
		l.mu.Unlock()
		for _, abort := range aborts {
			abort()
		}
		l.agents.release(claimed.Released...)
		for _, operation := range claimed.Operations {
			turn := operation.Operation.Kind == domain.ComputerOperationLocalAgent
			if !turn {
				l.slots <- struct{}{}
			}
			ctx, abort := context.WithCancel(l.ctx)
			l.mu.Lock()
			l.running[operation.ID] = abort
			l.mu.Unlock()
			running.Go(func() {
				defer func() {
					abort()
					l.mu.Lock()
					delete(l.running, operation.ID)
					l.mu.Unlock()
					if !turn {
						<-l.slots
					}
					// 释放槽位后再领取一次，排队的操作尽快开始。
					signal(l.wake)
				}()
				l.execute(ctx, operation)
			})
		}
	}
}

// execute 在操作的 ctx 内按服务端给出的时限执行一次操作并上报结果，时限为零时不设时限；委派本机 Agent 的轮次在本机 Agent 会话中执行，过程更新在结果之前上报完毕。
// 操作带有派发时的串联编号时，执行、同步与上报的日志和请求沿用该编号。
// 按中止要求提前结束且没有成功完成时上报为已中止；上报失败时按退避重试到成功或连接结束。
func (l *Link) execute(ctx context.Context, operation appservice.ComputerOperationItem) {
	// linkCtx 是连接存续期间的 context，用于中止之后仍需完成的同步与上报。
	ctx, linkCtx := withOperationTrace(ctx, operation), withOperationTrace(l.ctx, operation)
	slog.InfoContext(ctx, "开始执行电脑操作", "operation_id", operation.ID, "kind", operation.Operation.Kind)
	executeCtx, cancel := ctx, context.CancelFunc(func() {})
	if operation.TimeoutSeconds > 0 {
		executeCtx, cancel = context.WithTimeout(ctx, time.Duration(operation.TimeoutSeconds)*time.Second)
	}
	// 命令与本机 Agent 的一轮执行前后各同步一次共享文件区副本，执行后的同步在按中止要求结束时同样进行。
	synced := operation.Operation.Kind.SyncsSharedFiles()
	var report syncReport
	if synced {
		report = l.syncAround(executeCtx, operation)
	}
	var outcome domain.ComputerOutcome
	if operation.Operation.Kind == domain.ComputerOperationLocalAgent {
		outcome = l.runLocalAgent(executeCtx, operation)
	} else {
		outcome = l.options.Host.Execute(executeCtx, operation.Operation)
	}
	cancel()
	if synced {
		report.merge(l.syncAround(linkCtx, operation))
		outcome.SharedWrites = report.writes
		// 命令的结果交给模型，附上同步说明；本机 Agent 的回复原样展示给用户，不附加说明。
		if operation.Operation.Kind == domain.ComputerOperationCommand {
			if note := report.note(); note != "" && outcome.Error != "" {
				outcome.Error += note
			} else {
				outcome.Output += note
			}
		}
	}
	if l.ctx.Err() != nil {
		return
	}
	outcome.Aborted = ctx.Err() != nil && outcome.Error != ""
	_, err := backoff.Retry(linkCtx, func() (struct{}, error) {
		err := l.client.completeComputerOperation(linkCtx, operation.ID, appservice.ComputerOutcomeInput{Outcome: outcome})
		if err != nil && l.rejected(err) {
			return struct{}{}, backoff.Permanent(err)
		}
		return struct{}{}, err
	}, backoff.WithBackOff(newRetryBackOff()), backoff.WithMaxElapsedTime(0), backoff.WithNotify(func(err error, _ time.Duration) {
		slog.WarnContext(ctx, "上报电脑操作结果失败", "operation_id", operation.ID, "error", err)
	}))
	if err == nil {
		slog.InfoContext(ctx, "电脑操作已完成", "operation_id", operation.ID, "failed", outcome.Error != "", "aborted", outcome.Aborted)
	}
}

// withOperationTrace 在操作带有派发时的串联编号时返回记入该编号的 ctx，否则原样返回。
func withOperationTrace(ctx context.Context, operation appservice.ComputerOperationItem) context.Context {
	if operation.TraceID == "" {
		return ctx
	}
	return logscope.WithTrace(ctx, operation.TraceID)
}

// syncAround 同步一次操作的共享文件区副本，同步失败时记入结果并记录日志，不影响操作执行。
func (l *Link) syncAround(ctx context.Context, operation appservice.ComputerOperationItem) syncReport {
	report, err := l.syncShared(ctx, operation)
	if err != nil && !errors.Is(err, errSyncUnavailable) && l.ctx.Err() == nil {
		slog.WarnContext(ctx, "同步共享文件区副本失败", "operation_id", operation.ID, "error", err)
		report.failures = append(report.failures, err.Error())
	}
	return report
}

// reportLoop 在连接建立与执行能力变化时上报能力；失败时按退避重试，等待期间能力再次变化时立即重试，上报成功后退避从首次间隔开始，凭据失效或版本不一致时结束。
func (l *Link) reportLoop() {
	retry := newRetryBackOff()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-l.report:
		}
		for {
			err := l.reportCapabilities()
			if err == nil || l.ctx.Err() != nil {
				retry.Reset()
				break
			}
			if l.rejected(err) {
				return
			}
			slog.WarnContext(context.Background(), "上报电脑执行能力失败", "server", l.options.ServerURL, "error", err)
			select {
			case <-l.ctx.Done():
				return
			case <-l.report:
			case <-time.After(retry.NextBackOff()):
			}
		}
	}
}

// reportCapabilities 读取并上报当前平台与执行能力。
func (l *Link) reportCapabilities() error {
	capabilities, err := l.options.Host.Capabilities(l.ctx)
	if err != nil {
		return err
	}
	platform, _ := Platform()
	return l.client.reportComputerCapabilities(l.ctx, appservice.ComputerCapabilitiesInput{
		Platform: platform, Capabilities: capabilities, ExecutorVersion: domain.ExecutorVersion, MaxConcurrency: cap(l.slots),
	})
}

// signal 向容量为 1 的通道投递一次信号，已有待处理信号时直接返回。
func signal(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
