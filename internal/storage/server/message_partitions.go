//go:build server

package server

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

const (
	// messagePartitionMonthsAhead 是当前月份之后提前创建的消息分区月数。
	messagePartitionMonthsAhead = 3
)

// MessageMonthStart 返回时刻所在月份的 UTC 起点。
func MessageMonthStart(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// MessageIDLowerBound 返回指定时刻生成的 UUIDv7 消息编号的下界，编号按创建时刻的毫秒时间戳排序。
func MessageIDLowerBound(at time.Time) string {
	ms := at.UnixMilli()
	return fmt.Sprintf("%08x-%04x-0000-0000-000000000000", ms>>16, ms&0xffff)
}

// EnsureMessagePartitions 在咨询锁内为 from 所在月份及之后若干月份创建消息分区，分区按编号所含的创建月份划分；分区先建为独立表再挂载，挂载只对消息表加 SHARE UPDATE EXCLUSIVE 锁，与消息读写并行。
func EnsureMessagePartitions(ctx context.Context, db bun.IDB, from time.Time) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := XactLock(ctx, tx, LockMessagePartitions); err != nil {
			return err
		}
		month := MessageMonthStart(from)
		for range messagePartitionMonthsAhead + 1 {
			next := month.AddDate(0, 1, 0)
			name := "messages_" + month.Format("200601")
			var attached bool
			if err := tx.NewRaw("SELECT EXISTS (SELECT 1 FROM pg_inherits WHERE inhparent = 'public.messages'::regclass AND inhrelid = to_regclass(?))", "public."+name).Scan(ctx, &attached); err != nil {
				return fmt.Errorf("check message partition %s: %w", name, err)
			}
			if !attached {
				statements := []string{
					fmt.Sprintf("CREATE TABLE IF NOT EXISTS public.%s (LIKE public.messages INCLUDING DEFAULTS)", name),
					fmt.Sprintf("ALTER TABLE public.%s ALTER COLUMN search_vector SET STATISTICS 3000", name),
					fmt.Sprintf("ALTER TABLE public.messages ATTACH PARTITION public.%s FOR VALUES FROM ('%s') TO ('%s')", name, MessageIDLowerBound(month), MessageIDLowerBound(next)),
				}
				for _, statement := range statements {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return fmt.Errorf("create message partition %s: %w", name, err)
					}
				}
			}
			month = next
		}
		return nil
	})
}
