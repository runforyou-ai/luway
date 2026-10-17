//go:build server

package filemaintenance

import (
	"context"

	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/uptrace/bun"
)

const (
	// EnsurePartitionsActionName 是创建消息分区的 Action 名称。
	EnsurePartitionsActionName = "message.ensure_partitions"
	// PartitionsScheduleKey 是创建消息分区的定时计划标识。
	PartitionsScheduleKey = "message.partitions"
)

// EnsurePartitionsInput 定义创建消息分区的输入。
type EnsurePartitionsInput struct{}

// EnsurePartitionsAction 提前创建当前及之后月份的消息分区。
type EnsurePartitionsAction struct{ db *bun.DB }

// NewEnsurePartitionsAction 创建消息分区维护 Action。
func NewEnsurePartitionsAction(db *bun.DB) *EnsurePartitionsAction {
	return &EnsurePartitionsAction{db: db}
}

// Execute 为数据库当前月份及之后若干月份创建尚不存在的消息分区。
func (a *EnsurePartitionsAction) Execute(ctx context.Context, _ EnsurePartitionsInput) error {
	now, err := serverstorage.Now(ctx, a.db)
	if err != nil {
		return err
	}
	return serverstorage.EnsureMessagePartitions(ctx, a.db, now)
}
