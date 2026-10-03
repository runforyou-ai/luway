//go:build !server && !ios && !android

package executor

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
)

const (
	// pollInterval 是不依赖事件流、定期领取操作的间隔。
	pollInterval = 30 * time.Second
	// retryInterval 是请求失败后首次重试的间隔，连续失败按倍数退避到 maxRetryInterval。
	retryInterval = 5 * time.Second
	// maxRetryInterval 是连续失败后的最长重试间隔。
	maxRetryInterval = time.Minute
	// streamIdleTimeout 是事件流未收到任何事件即断开重连的时限，服务端每 25 秒发送心跳。
	streamIdleTimeout = 60 * time.Second
	// streamMaxLineBytes 是事件流单行事件的读取上限。
	streamMaxLineBytes = 1 << 20
	// defaultConcurrency 是同时执行的操作数。
	defaultConcurrency = 4
)

// Version 是执行器支持的操作原语版本，新增操作原语时加一。
const Version = "1"

// LinkOptions 定义一条执行器连接：服务器地址、电脑凭据与执行环境，凭据失效时调用 OnCredentialInvalid。
type LinkOptions struct {
	ServerURL           string
	Credential          string
	Host                *Host
	OnCredentialInvalid func()
}

// Link 以电脑身份连接一个工作区：维持事件流记录在线，领取派发给这台电脑的操作并上报结果，执行能力变化时重新上报。
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
	// mu 保护 running。
	mu sync.Mutex
	// running 是本机正在执行或尚未上报结果的操作编号。
	running map[string]struct{}
}

// StartLink 建立执行器连接并开始事件流、领取与能力上报循环；服务器地址无效时返回错误。
func StartLink(options LinkOptions) (*Link, error) {
	client, err := newClient(options.ServerURL, options.Credential)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	link := &Link{
		options: options, client: client, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), report: make(chan struct{}, 1), slots: make(chan struct{}, defaultConcurrency),
		running: map[string]struct{}{},
	}
	link.unsubscribe = options.Host.Subscribe(func() { signal(link.report) })
	signal(link.report)
	link.loops.Go(link.listen)
	link.loops.Go(link.work)
	link.loops.Go(link.reportLoop)
	return link, nil
}

// Stop 结束连接、取消能力变化登记并等待执行中的操作退出。
func (l *Link) Stop() {
	l.cancel()
	l.unsubscribe()
	l.loops.Wait()
}

// heldOperations 返回本机正在执行或尚未上报结果的操作编号。
func (l *Link) heldOperations() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Collect(maps.Keys(l.running))
}

// credentialInvalid 在凭据失效时结束连接并通知一次。
func (l *Link) credentialInvalid() {
	l.revoke.Do(func() {
		slog.Warn("电脑凭据已失效，停止执行器连接", "server", l.options.ServerURL)
		l.cancel()
		if l.options.OnCredentialInvalid != nil {
			go l.options.OnCredentialInvalid()
		}
	})
}

// listen 维持事件流直到连接结束，收到连接确认或待执行操作通知时领取操作，连接失败按退避重连。
func (l *Link) listen() {
	backoff := retryInterval
	for {
		connected := l.stream()
		if l.ctx.Err() != nil {
			return
		}
		wait := retryInterval
		if connected {
			backoff = retryInterval
		} else {
			wait = backoff
			backoff = min(backoff*2, maxRetryInterval)
		}
		select {
		case <-l.ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// stream 建立一次事件流并读取到结束，返回是否曾连接成功。
func (l *Link) stream() bool {
	body, err := l.client.openStream(l.ctx)
	if errors.Is(err, ErrCredentialInvalid) {
		l.credentialInvalid()
		return false
	}
	if err != nil {
		if l.ctx.Err() == nil {
			slog.Warn("建立执行器事件流失败", "server", l.options.ServerURL, "error", err)
		}
		return false
	}
	defer body.Close()
	// 空闲超时或连接结束时关闭事件流，读取随之结束。
	idle := time.AfterFunc(streamIdleTimeout, func() { body.Close() })
	defer idle.Stop()
	stop := context.AfterFunc(l.ctx, func() { body.Close() })
	defer stop()
	slog.Info("执行器事件流已建立", "server", l.options.ServerURL)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), streamMaxLineBytes)
	for scanner.Scan() {
		idle.Reset(streamIdleTimeout)
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		frame, err := protocol.Decode([]byte(data))
		if err != nil {
			continue
		}
		switch frame.(type) {
		case protocol.ServerHello, protocol.ComputerWork:
			// 重新连接期间可能错过通知，连接建立后立即领取一次。
			signal(l.wake)
		}
	}
	slog.Info("执行器事件流已结束", "server", l.options.ServerURL)
	return true
}

// work 在通知与定时器上领取操作，按空闲槽位数领取并并发执行，直到连接结束。
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
		free := cap(l.slots) - len(l.slots)
		if free == 0 {
			continue
		}
		// 每次领取都带上本机仍持有的操作，服务端据此结算已无人执行的调用，定时领取同时起到核对作用。
		operations, err := l.client.claim(l.ctx, free, l.heldOperations())
		if errors.Is(err, ErrCredentialInvalid) {
			l.credentialInvalid()
			return
		}
		if err != nil {
			if l.ctx.Err() == nil {
				slog.Warn("领取电脑操作失败", "server", l.options.ServerURL, "error", err)
			}
			continue
		}
		for _, operation := range operations {
			l.slots <- struct{}{}
			l.mu.Lock()
			l.running[operation.ID] = struct{}{}
			l.mu.Unlock()
			running.Go(func() {
				defer func() {
					l.mu.Lock()
					delete(l.running, operation.ID)
					l.mu.Unlock()
					<-l.slots
					// 释放槽位后再领取一次，排队的操作尽快开始。
					signal(l.wake)
				}()
				l.execute(operation)
			})
		}
	}
}

// execute 执行一次操作并上报结果，上报失败时按退避重试到成功或连接结束。
func (l *Link) execute(operation appservice.ComputerOperationItem) {
	slog.Info("开始执行电脑操作", "operation_id", operation.ID, "kind", operation.Operation.Kind)
	outcome := l.options.Host.Execute(l.ctx, operation.Operation)
	if l.ctx.Err() != nil {
		return
	}
	backoff := retryInterval
	for {
		err := l.client.complete(l.ctx, operation.ID, appservice.ComputerOutcomeInput{Outcome: outcome})
		if err == nil {
			slog.Info("电脑操作已完成", "operation_id", operation.ID, "failed", outcome.Error != "")
			return
		}
		if errors.Is(err, ErrCredentialInvalid) {
			l.credentialInvalid()
			return
		}
		slog.Warn("上报电脑操作结果失败", "operation_id", operation.ID, "error", err)
		select {
		case <-l.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxRetryInterval)
	}
}

// reportLoop 在连接建立与执行能力变化时上报能力，失败时按退避重试。
func (l *Link) reportLoop() {
	backoff := retryInterval
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-l.report:
		}
		for {
			err := l.reportCapabilities()
			if err == nil || l.ctx.Err() != nil {
				backoff = retryInterval
				break
			}
			if errors.Is(err, ErrCredentialInvalid) {
				l.credentialInvalid()
				return
			}
			slog.Warn("上报电脑执行能力失败", "server", l.options.ServerURL, "error", err)
			select {
			case <-l.ctx.Done():
				return
			case <-l.report:
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxRetryInterval)
		}
	}
}

// reportCapabilities 读取并上报当前执行能力。
func (l *Link) reportCapabilities() error {
	capabilities, err := l.options.Host.Capabilities(l.ctx)
	if err != nil {
		return err
	}
	return l.client.reportCapabilities(l.ctx, appservice.ComputerCapabilitiesInput{
		Capabilities: capabilities, ExecutorVersion: Version, MaxConcurrency: cap(l.slots),
	})
}

// signal 向容量为 1 的通道投递一次信号，已有待处理信号时直接返回。
func signal(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
