//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestTransactionAfterCommit 验证提交后回调在提交成功后按登记顺序执行，事务失败、保存点回滚或 panic 时丢弃，回调失败不影响其余回调。
func TestTransactionAfterCommit(t *testing.T) {
	t.Parallel()
	db := openRealtimeDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS transaction_probes (id text PRIMARY KEY)")
	require.NoError(t, err)

	// insert 在事务内写入一行探针并返回其编号。
	insert := func(ctx context.Context, tx bun.IDB) string {
		id := uuid.NewV7().String()
		_, err := tx.ExecContext(ctx, "INSERT INTO transaction_probes (id) VALUES (?)", id)
		require.NoError(t, err)
		return id
	}
	// committed 经连接池读取探针，返回其是否已对其他连接可见。
	committed := func(id string) bool {
		exists, err := db.NewSelect().TableExpr("transaction_probes").Where("id = ?", id).Exists(ctx)
		require.NoError(t, err)
		return exists
	}

	t.Run("提交后按登记顺序执行", func(t *testing.T) {
		var order []string
		var id string
		require.NoError(t, serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
			id = insert(ctx, tx)
			serverstorage.AfterCommit(ctx, func(context.Context) error {
				order = append(order, "first")
				require.True(t, committed(id), "回调执行时事务已提交")
				return nil
			})
			serverstorage.AfterCommit(ctx, func(context.Context) error {
				order = append(order, "second")
				return nil
			})
			require.Empty(t, order, "提交前不执行回调")
			return nil
		}))
		require.Equal(t, []string{"first", "second"}, order)
	})

	t.Run("事务失败时丢弃回调", func(t *testing.T) {
		called := false
		failure := errors.New("业务失败")
		err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
			insert(ctx, tx)
			serverstorage.AfterCommit(ctx, func(context.Context) error {
				called = true
				return nil
			})
			return failure
		})
		require.ErrorIs(t, err, failure)
		require.False(t, called)
	})

	t.Run("保存点内的回调随外层提交执行，保存点回滚时丢弃", func(t *testing.T) {
		var order []string
		var kept, dropped string
		require.NoError(t, serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
			require.NoError(t, serverstorage.RunInTx(ctx, tx, func(ctx context.Context, tx bun.Tx) error {
				kept = insert(ctx, tx)
				serverstorage.AfterCommit(ctx, func(context.Context) error {
					order = append(order, "kept")
					require.True(t, committed(kept), "保存点回调在外层事务提交后执行")
					return nil
				})
				return nil
			}))
			require.Empty(t, order, "保存点结束时不执行回调")
			rollback := errors.New("保存点回滚")
			err := serverstorage.RunInTx(ctx, tx, func(ctx context.Context, tx bun.Tx) error {
				dropped = insert(ctx, tx)
				serverstorage.AfterCommit(ctx, func(context.Context) error {
					order = append(order, "dropped")
					return nil
				})
				return rollback
			})
			require.ErrorIs(t, err, rollback)
			return nil
		}))
		require.Equal(t, []string{"kept"}, order)
		require.False(t, committed(dropped), "回滚的保存点不留下写入")
	})

	t.Run("保存点内 panic 被恢复时丢弃其中登记的回调", func(t *testing.T) {
		var order []string
		require.NoError(t, serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
			// 保存点 panic 后由本事务函数恢复，外层事务继续提交。
			func() {
				defer func() { _ = recover() }()
				_ = serverstorage.RunInTx(ctx, tx, func(ctx context.Context, tx bun.Tx) error {
					serverstorage.AfterCommit(ctx, func(context.Context) error {
						order = append(order, "dropped")
						return nil
					})
					panic("保存点异常")
				})
			}()
			serverstorage.AfterCommit(ctx, func(context.Context) error {
				order = append(order, "outer")
				return nil
			})
			return nil
		}))
		require.Equal(t, []string{"outer"}, order)
	})

	t.Run("回调失败时继续执行其余回调", func(t *testing.T) {
		var order []string
		require.NoError(t, serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
			insert(ctx, tx)
			serverstorage.AfterCommit(ctx, func(context.Context) error {
				order = append(order, "error")
				return errors.New("回调失败")
			})
			serverstorage.AfterCommit(ctx, func(context.Context) error {
				order = append(order, "panic")
				panic("回调异常")
			})
			serverstorage.AfterCommit(ctx, func(context.Context) error {
				order = append(order, "last")
				return nil
			})
			return nil
		}))
		require.Equal(t, []string{"error", "panic", "last"}, order)
	})
}
