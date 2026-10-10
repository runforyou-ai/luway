//go:build server

package broker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/jetcast"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// MaxConnectionTTL 是成员 NATS 凭据的最长有效期，也是强制关闭补偿的时间上界；客户端在到期前两分钟换用新连接并重新认证，提前量覆盖浏览器后台页面每分钟一次的定时器节流。
const MaxConnectionTTL = 20 * time.Minute

const revokeAction = "realtime.kick"
const revokeInterval = 5 * time.Second

// standaloneAdmin 管理独立内嵌 NATS，存储中其他服务器编号的连接属于已退出的实例。
type standaloneAdmin struct {
	jetcast.ConnectionAdmin
	serverID string
}

// Kick 将已退出实例的撤销视为完成，仅向当前实例发送连接关闭请求。
func (a standaloneAdmin) Kick(ctx context.Context, serverID string, cid uint64) error {
	if serverID != a.serverID {
		return nil
	}
	return a.ConnectionAdmin.Kick(ctx, serverID, cid)
}

// kickInput 持久保存原始 NATS 连接定位和补偿截止时间。
type kickInput struct {
	ServerID string    `json:"serverId"`
	CID      uint64    `json:"cid"`
	Until    time.Time `json:"until"`
}

// revocationAdmin 先持久登记精确连接的补偿任务，再立即尝试强制关闭。
type revocationAdmin struct {
	admin jetcast.ConnectionAdmin
	tasks servertask.Enqueuer
	db    *bun.DB
}

// RegisterRevocations 注册平台级强制关闭任务，并为成员服务安装可靠撤销适配器。
func (b *Connection) RegisterRevocations(tasks *servertask.Runtime, db *bun.DB) error {
	admin := &revocationAdmin{admin: b.Admin, tasks: tasks, db: db}
	if err := tasks.Registry().RegisterJSON(revokeAction, admin.execute); err != nil {
		return err
	}
	b.Admin = admin
	return nil
}

// Kick 持久登记原连接的补偿，再立即执行关闭；任一步失败都向调用方传播。
func (a *revocationAdmin) Kick(ctx context.Context, serverID string, cid uint64) error {
	// 补偿登记使用独立时限，按原始连接持久化已提交的撤销意图。
	registerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeInterval)
	defer cancel()
	now, err := serverstorage.ClockNow(registerCtx, a.db)
	if err != nil {
		return errors.Join(err, a.admin.Kick(ctx, serverID, cid))
	}
	err = a.tasks.Enqueue(registerCtx, revokeAction, kickInput{ServerID: serverID, CID: cid, Until: now.Add(MaxConnectionTTL)}, servertask.EnqueueOptions{
		Queue: servertask.QueueMaintenance, Delay: revokeInterval,
		IdempotencyKey: fmt.Sprintf("%s/%d", serverID, cid),
		// 每次失败至少延迟一个间隔，重试预算覆盖完整凭据有效期及截止检查。
		MaxAttempts: int(MaxConnectionTTL/revokeInterval) + 2,
	})
	if err != nil {
		err = fmt.Errorf("persist member connection revocation %s/%d: %w", serverID, cid, err)
	}
	return errors.Join(err, a.admin.Kick(ctx, serverID, cid))
}

// execute 只关闭记录的旧连接，按固定间隔重试直到关闭成功或凭据必然过期。
func (a *revocationAdmin) execute(ctx context.Context, input kickInput) error {
	now, err := serverstorage.ClockNow(ctx, a.db)
	if err != nil {
		return servertask.RetryAfter(revokeInterval, err)
	}
	if !now.Before(input.Until) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, revokeInterval)
	defer cancel()
	if err := a.admin.Kick(ctx, input.ServerID, input.CID); err != nil {
		return servertask.RetryAfter(revokeInterval, err)
	}
	return nil
}
