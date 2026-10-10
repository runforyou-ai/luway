package server

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// afterCommitKey 是上下文中当前写事务提交后回调登记表的键。
type afterCommitKey struct{}

// afterCommitHooks 按登记顺序保存一个顶层事务的提交后回调，tx 是该事务的数据库事务。
type afterCommitHooks struct {
	tx        *sql.Tx
	callbacks []func(context.Context) error
}

// RunInTx 执行写事务并在提交成功后按登记顺序执行事务内经 AfterCommit 登记的回调，回滚或提交失败时丢弃回调。
// db 为 *bun.DB 或 bun.Conn 时开启独立的顶层事务并使用新的登记表，与上下文中外层事务的登记表互不影响；
// db 为 bun.Tx 时以保存点执行：外层事务由 RunInTx 开启时加入其登记表，回调在外层事务提交后执行，保存点回滚时丢弃保存点内登记的回调；外层事务未经 RunInTx 开启时保存点内不可登记回调。
// 回调执行时事务结果已确定，回调返回错误或 panic 时记录 Error 日志后继续执行其余回调，RunInTx 仍返回 nil。
func RunInTx(ctx context.Context, db bun.IDB, fn func(context.Context, bun.Tx) error) error {
	if tx, ok := db.(bun.Tx); ok {
		hooks, _ := ctx.Value(afterCommitKey{}).(*afterCommitHooks)
		// 只加入同一数据库事务的登记表，其余情况下保存点内不可登记回调。
		if hooks != nil && hooks.tx != tx.Tx {
			hooks = nil
		}
		mark := 0
		if hooks != nil {
			mark = len(hooks.callbacks)
		}
		inner := context.WithValue(ctx, afterCommitKey{}, hooks)
		// 保存点返回错误或 panic 时都已回滚，截断保存点内登记的回调。
		released := false
		defer func() {
			if !released && hooks != nil {
				hooks.callbacks = hooks.callbacks[:mark]
			}
		}()
		err := tx.RunInTx(inner, nil, func(_ context.Context, savepoint bun.Tx) error { return fn(inner, savepoint) })
		released = err == nil
		return err
	}
	hooks := &afterCommitHooks{}
	err := db.RunInTx(context.WithValue(ctx, afterCommitKey{}, hooks), nil, func(ctx context.Context, tx bun.Tx) error {
		hooks.tx = tx.Tx
		return fn(ctx, tx)
	})
	if err != nil {
		return err
	}
	// 回调使用去除取消信号、解除事务登记表关联的调用方上下文。
	callbackCtx := context.WithValue(context.WithoutCancel(ctx), afterCommitKey{}, (*afterCommitHooks)(nil))
	for _, callback := range hooks.callbacks {
		runAfterCommit(callbackCtx, callback)
	}
	return nil
}

// AfterCommit 在当前 RunInTx 事务内登记提交后回调；调用方必须处于 RunInTx 内。实时通知与任务投递经它在提交后发布，进程在提交后、回调前退出时回调不执行。
func AfterCommit(ctx context.Context, callback func(context.Context) error) {
	hooks, _ := ctx.Value(afterCommitKey{}).(*afterCommitHooks)
	if hooks == nil {
		panic("serverstorage: AfterCommit called outside serverstorage.RunInTx")
	}
	hooks.callbacks = append(hooks.callbacks, callback)
}

// runAfterCommit 执行单个提交后回调，返回错误或 panic 时记录 Error 日志。
func runAfterCommit(ctx context.Context, callback func(context.Context) error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(ctx, "事务提交后回调失败", "error", support.NewPanicError(recovered))
		}
	}()
	if err := callback(ctx); err != nil {
		slog.ErrorContext(ctx, "事务提交后回调失败", "error", err)
	}
}
