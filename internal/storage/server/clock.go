package server

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// Now 返回数据库的当前时刻；在事务中调用时等于该事务内 SQL 的 now()。
func Now(ctx context.Context, db bun.IDB) (time.Time, error) {
	var now time.Time
	if err := db.NewRaw("SELECT now()").Scan(ctx, &now); err != nil {
		return time.Time{}, fmt.Errorf("read database time: %w", err)
	}
	return now, nil
}

// ClockNow 返回数据库此刻的时间（clock_timestamp()），用于长事务中或等待行锁之后需要最新时刻的判断。
func ClockNow(ctx context.Context, db bun.IDB) (time.Time, error) {
	var now time.Time
	if err := db.NewRaw("SELECT clock_timestamp()").Scan(ctx, &now); err != nil {
		return time.Time{}, fmt.Errorf("read database clock: %w", err)
	}
	return now, nil
}
