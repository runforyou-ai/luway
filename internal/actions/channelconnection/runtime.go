//go:build server

// Package channelconnection 维持长连接渠道的平台连接：按渠道的任务路由租约在单个服务端实例上建立连接，
// 入站事件任务与外发任务都按同一路由由持有连接的实例认领，平台交互经本机连接完成。
package channelconnection

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/channelinbound"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

const (
	// reconcileInterval 是核对租约与连接的间隔。
	reconcileInterval = 10 * time.Second
	// leaseDuration 是每次取得或续约的渠道路由租约时长。
	leaseDuration = 30 * time.Second
	// unavailableRetryAfter 是本实例没有在线连接时外发等待重发的时间。
	unavailableRetryAfter = 5 * time.Second
)

// 连接状态取值。
const (
	StatusConnecting = "connecting"
	StatusOnline     = "online"
	StatusReplaced   = "replaced"
	StatusRejected   = "rejected"
	StatusOffline    = "offline"
)

// Tasks 是长连接运行时使用的任务运行时能力：写入入站事件任务并取得、续约与释放渠道的路由租约。
type Tasks interface {
	servertask.Enqueuer
	InstanceID() string
	HoldRoute(ctx context.Context, key string, lease time.Duration) (bool, error)
	ReleaseRoute(ctx context.Context, key string) error
}

// Runtime 在本服务端实例上维持所持路由租约渠道的长连接。
type Runtime struct {
	db       *bun.DB
	adapters *channeladapter.Registry
	tasks    Tasks

	mu       sync.Mutex
	sessions map[string]*heldSession
	cancel   context.CancelFunc
	done     chan struct{}
}

// heldSession 是本实例持有租约的一个渠道连接。
type heldSession struct {
	target channeladapter.Target
	// revision 是本次连接所用的配置版本。
	revision string
	cancel   context.CancelFunc
	done     chan struct{}
	// session 是已建立的连接，建立前与断开后为空。
	session channeladapter.Session
	// halted 表示连接因凭据被拒或被其他连接接管而停止。
	halted bool
}

// New 创建长连接运行时，以任务运行时的实例编号持有渠道路由租约。
func New(db *bun.DB, adapters *channeladapter.Registry, tasks Tasks) *Runtime {
	return &Runtime{db: db, adapters: adapters, tasks: tasks, sessions: map[string]*heldSession{}}
}

// Start 启动按间隔核对租约与连接的循环。
func (r *Runtime) Start(ctx context.Context) {
	ctx, r.cancel = context.WithCancel(context.WithoutCancel(ctx))
	r.done = make(chan struct{})
	slog.InfoContext(ctx, "渠道长连接运行时已启动", "instance_id", r.tasks.InstanceID())
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(reconcileInterval)
		defer ticker.Stop()
		for {
			if err := r.reconcile(ctx); err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "核对渠道长连接失败", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop 关闭本实例持有的全部连接并释放租约，其他实例随后接管。
func (r *Runtime) Stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
	r.mu.Lock()
	held := r.sessions
	r.sessions = map[string]*heldSession{}
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for channelID, session := range held {
		r.release(ctx, channelID, session)
	}
	slog.InfoContext(ctx, "渠道长连接运行时已停止", "instance_id", r.tasks.InstanceID(), "released_channel_count", len(held))
}

// connectorChannel 是需要维持长连接的渠道。
type connectorChannel struct {
	ID          string             `bun:"id"`
	WorkspaceID string             `bun:"workspace_id"`
	Type        domain.ChannelType `bun:"type"`
	AccountID   string             `bun:"provider_account_id"`
}

// reconcile 为需要连接的渠道取得或续约路由租约，按租约与配置版本启动、重建或停止连接。
func (r *Runtime) reconcile(ctx context.Context) error {
	types := arr.Filter(domain.ChannelTypesWith(func(capabilities domain.ChannelCapabilities) bool { return capabilities.Connection }), func(channelType domain.ChannelType) bool {
		_, ok := channeladapter.Lookup[channeladapter.Connector](r.adapters, channelType)
		return ok
	})
	var channels []connectorChannel
	if len(types) > 0 {
		if err := r.db.NewSelect().TableExpr("channels AS c").Column("c.id", "c.workspace_id", "c.type", "c.provider_account_id").
			Where("c.type IN (?) AND c.enabled AND c.provider_account_id IS NOT NULL", bun.List(types)).
			Where(identityaction.ActiveWorkspaceCondition("c.workspace_id")).
			Scan(ctx, &channels); err != nil {
			return err
		}
	}
	wanted := map[string]bool{}
	for _, channel := range channels {
		held, err := r.tasks.HoldRoute(ctx, channeladapter.ConnectionRoute(channel.ID), leaseDuration)
		if err != nil {
			// 续约失败时租约仍在有效期内，保留已有连接等待下次核对。
			r.mu.Lock()
			wanted[channel.ID] = r.sessions[channel.ID] != nil
			r.mu.Unlock()
			slog.WarnContext(ctx, "取得渠道长连接租约失败", "channel_id", channel.ID, "error", err)
			continue
		}
		if !held {
			continue
		}
		wanted[channel.ID] = true
		r.ensureSession(ctx, channel)
	}
	// 本轮不需要连接或未持有租约的渠道关闭连接并释放租约。
	r.mu.Lock()
	var stale []string
	for channelID := range r.sessions {
		if !wanted[channelID] {
			stale = append(stale, channelID)
		}
	}
	r.mu.Unlock()
	for _, channelID := range stale {
		r.mu.Lock()
		session := r.sessions[channelID]
		delete(r.sessions, channelID)
		r.mu.Unlock()
		r.release(ctx, channelID, session)
	}
	return nil
}

// ensureSession 在没有连接、连接所用配置版本已过时，或连接中断时建立连接；凭据被拒或被接管而停止的连接在配置版本变化前不重连。
func (r *Runtime) ensureSession(ctx context.Context, channel connectorChannel) {
	connector, _ := channeladapter.Lookup[channeladapter.Connector](r.adapters, channel.Type)
	target := channeladapter.Target{WorkspaceID: channel.WorkspaceID, ChannelID: channel.ID, AccountID: channel.AccountID}
	revision, err := connector.Revision(ctx, target)
	if err != nil {
		slog.WarnContext(ctx, "读取渠道长连接配置失败", "channel_id", channel.ID, "error", err)
		return
	}
	r.mu.Lock()
	current := r.sessions[channel.ID]
	r.mu.Unlock()
	if current != nil {
		finished := false
		select {
		case <-current.done:
			finished = true
		default:
		}
		r.mu.Lock()
		halted := current.halted
		r.mu.Unlock()
		if current.revision == revision && (!finished || halted) {
			return
		}
		current.cancel()
		<-current.done
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	held := &heldSession{target: target, revision: revision, cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.sessions[channel.ID] = held
	r.mu.Unlock()
	go r.run(sessionCtx, connector, held)
}

// run 建立连接、登记为可外发的连接并接收事件，连接结束时记录状态。
func (r *Runtime) run(ctx context.Context, connector channeladapter.Connector, held *heldSession) {
	defer close(held.done)
	channelID := held.target.ChannelID
	r.setStatus(ctx, held.target, StatusConnecting, "", false)
	session, err := connector.Connect(ctx, held.target)
	if err != nil {
		r.finish(ctx, held, err)
		return
	}
	defer session.Close()
	r.mu.Lock()
	held.session = session
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		held.session = nil
		r.mu.Unlock()
	}()
	r.setStatus(ctx, held.target, StatusOnline, "", true)
	slog.InfoContext(ctx, "渠道长连接已建立", "channel_id", channelID, "provider_account_id", session.AccountID())
	err = session.Serve(ctx, func(event channeladapter.InboundEvent) {
		r.receive(ctx, held.target, session.AccountID(), event)
	})
	r.finish(ctx, held, err)
}

// finish 按连接结束原因记录状态，被拒或被接管的连接标为停止；ctx 已结束表示本实例主动关闭，不记录。
func (r *Runtime) finish(ctx context.Context, held *heldSession, err error) {
	if ctx.Err() != nil {
		return
	}
	channelID := held.target.ChannelID
	status, code := StatusOffline, "connection_lost"
	switch {
	case errors.Is(err, channeladapter.ErrConnectionRejected):
		status, code = StatusRejected, "credentials_rejected"
	case errors.Is(err, channeladapter.ErrConnectionReplaced):
		status, code = StatusReplaced, "connection_replaced"
	}
	r.mu.Lock()
	held.halted = status != StatusOffline
	r.mu.Unlock()
	r.setStatus(context.WithoutCancel(ctx), held.target, status, code, false)
	slog.WarnContext(ctx, "渠道长连接已断开", "channel_id", channelID, "status", status, "error", err)
}

// setStatus 在本实例仍持有渠道路由租约时写入连接状态。
func (r *Runtime) setStatus(ctx context.Context, target channeladapter.Target, status, code string, online bool) {
	_, err := r.db.NewRaw(`INSERT INTO channel_connections (channel_id, workspace_id, status, last_error, connected_at)
		SELECT ?, ?, ?, ?, CASE WHEN ? THEN now() END WHERE ?
		ON CONFLICT (channel_id) DO UPDATE SET
			status = EXCLUDED.status, last_error = EXCLUDED.last_error, connected_at = EXCLUDED.connected_at`,
		target.ChannelID, target.WorkspaceID, status, code, online,
		servertask.RouteHeldBy(channeladapter.ConnectionRoute(target.ChannelID), r.tasks.InstanceID())).Exec(ctx)
	if err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "写入渠道长连接状态失败", "channel_id", target.ChannelID, "error", err)
	}
}

// release 关闭连接，在本实例仍持有路由租约时删除连接状态，再释放租约。
func (r *Runtime) release(ctx context.Context, channelID string, held *heldSession) {
	if held != nil {
		held.cancel()
		<-held.done
	}
	route := channeladapter.ConnectionRoute(channelID)
	if _, err := r.db.NewDelete().TableExpr("channel_connections").Where("channel_id = ?", channelID).
		Where("?", servertask.RouteHeldBy(route, r.tasks.InstanceID())).Exec(ctx); err != nil {
		slog.WarnContext(ctx, "删除渠道长连接状态失败", "channel_id", channelID, "error", err)
	}
	if err := r.tasks.ReleaseRoute(ctx, route); err != nil {
		slog.WarnContext(ctx, "释放渠道长连接租约失败", "channel_id", channelID, "error", err)
	}
}

// Send 经本实例持有的渠道连接发送外发请求项并返回归一化结果；本实例没有该渠道的在线连接时等待重发，渠道已改连其他平台账号时不发送。
func (r *Runtime) Send(ctx context.Context, target channeladapter.Target, request channeladapter.Request) channeladapter.Result {
	r.mu.Lock()
	var session channeladapter.Session
	if held := r.sessions[target.ChannelID]; held != nil {
		session = held.session
	}
	r.mu.Unlock()
	switch {
	case session == nil:
		return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: "connection_unavailable", RetryAfter: unavailableRetryAfter}
	case target.AccountID != session.AccountID():
		return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "account_changed"}
	}
	return session.Send(ctx, target, request)
}

// receive 把入站事件写入任务队列，按渠道与平台事件编号排重；事件按渠道长连接路由由持有连接的实例处理，回复提示经本机连接发送。
func (r *Runtime) receive(ctx context.Context, target channeladapter.Target, accountID string, event channeladapter.InboundEvent) {
	if event.ID == "" || event.Sender == "" {
		return
	}
	if err := channelinbound.EnqueueEvent(ctx, r.tasks, channelinbound.ReceiveEventInput{
		WorkspaceID: target.WorkspaceID, ChannelID: target.ChannelID, AccountID: accountID, Event: event,
	}, channeladapter.ConnectionRoute(target.ChannelID)); err != nil {
		slog.ErrorContext(ctx, "写入渠道入站事件失败", "channel_id", target.ChannelID, "event_id", event.ID, "error", err)
	}
}
