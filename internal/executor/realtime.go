//go:build !server && !ios && !android

package executor

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"sync/atomic"
	"time"

	jetclient "github.com/runforyou-ai/jetcast/client"
)

// connectRealtime 维持正式 SDK 工作通知连接，连接结束时显式释放全部订阅和重试。
func (l *Link) connectRealtime() bool {
	config, err := l.client.getComputerRealtimeConnection(l.ctx)
	if l.rejected(err) || err != nil {
		return false
	}
	address, err := url.Parse(l.client.resolve(config.Path))
	if err != nil {
		return false
	}
	if address.Scheme == "https" {
		address.Scheme = "wss"
	} else {
		address.Scheme = "ws"
	}
	echo, err := jetclient.Connect(l.ctx, jetclient.Options{Servers: []string{address.String()}, Prefix: config.Prefix, GetToken: func(ctx context.Context) (string, error) {
		// SDK 的令牌回调同时受连接代次和 SDK 自身截止时间约束。
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(l.ctx, cancel)
		defer stop()
		defer cancel()
		current, err := l.client.getComputerRealtimeConnection(ctx)
		if l.rejected(err) {
			return "", errors.Join(jetclient.ErrUnauthorized, err)
		}
		return current.Token, err
	}})
	if err != nil {
		return false
	}
	defer echo.Close()
	if l.ctx.Err() != nil {
		return false
	}
	attempt, cancel := context.WithCancel(l.ctx)
	defer cancel()
	var denied atomic.Bool
	echo.OnStatus(func(status jetclient.Status) {
		if status == jetclient.StatusStopped {
			cancel()
		}
	})
	subscription := echo.Private(config.Channel)
	subscription.ListenAll(func(jetclient.Event) { signal(l.wake) })
	subscription.OnState(func(state jetclient.State) {
		if state.State == jetclient.StateSubscribed {
			signal(l.wake)
		}
		if state.State == jetclient.StateDenied {
			denied.Store(true)
			cancel()
		}
	})
	if echo.Status() == jetclient.StatusStopped {
		return false
	}
	if err := subscription.Ready(attempt); err != nil {
		return false
	}
	signal(l.wake)
	if echo.Status() == jetclient.StatusStopped {
		return true
	}
	<-attempt.Done()
	return !denied.Load()
}

// heartbeat 独立于消息连接以已认证 HTTP 请求维护电脑在线状态。
func (l *Link) heartbeat() {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		err := l.client.heartbeatComputer(l.ctx)
		if l.rejected(err) || l.ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.WarnContext(l.ctx, "电脑心跳失败", "server", l.options.ServerURL, "error", err)
		}
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
