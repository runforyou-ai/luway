//go:build server

// Package outbox 将数据库内持久登记的消息投递到内部消息服务。
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/uptrace/bun"
)

// Dispatcher 串行投递领取的消息批次，确认成功后删除登记。
type Dispatcher struct {
	db      *bun.DB
	kind    string
	publish func(context.Context, string, json.RawMessage) error
	cancel  context.CancelFunc
	wait    sync.WaitGroup
}

// New 创建指定消息类型的持久投递器。
func New(db *bun.DB, kind string, publish func(context.Context, string, json.RawMessage) error) *Dispatcher {
	return &Dispatcher{db: db, kind: kind, publish: publish}
}

// Start 启动投递循环，行锁使多个实例领取不同批次。
func (d *Dispatcher) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	d.cancel = cancel
	d.wait.Go(func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			found, err := d.Step(ctx)
			if err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "持久消息投递失败", "kind", d.kind, "error", err)
			}
			if found && err == nil {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

// Stop 停止领取并等待当前发布结束。
func (d *Dispatcher) Stop() {
	if d.cancel != nil {
		d.cancel()
		d.wait.Wait()
	}
}

// Step 在一个有界事务内领取并投递一个批次，失败登记按数据库时钟重试。
func (d *Dispatcher) Step(parent context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	found := false
	var publishErr error
	err := d.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var row struct {
			ID      string
			Payload json.RawMessage
		}
		err := tx.NewRaw("SELECT id::text, payload FROM message_outbox WHERE kind = ? AND available_at <= now() ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1", d.kind).Scan(ctx, &row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		publishCtx, end := context.WithTimeout(ctx, 5*time.Second)
		publishErr = d.publish(publishCtx, row.ID, row.Payload)
		end()
		if publishErr != nil {
			_, err = tx.NewRaw("UPDATE message_outbox SET attempts = attempts + 1, available_at = now() + make_interval(secs => least(30, power(2, least(attempts, 5)))) WHERE id = ?", row.ID).Exec(ctx)
		} else {
			_, err = tx.NewRaw("DELETE FROM message_outbox WHERE id = ?", row.ID).Exec(ctx)
		}
		return err
	})
	return found, errors.Join(err, publishErr)
}
