//go:build server

// Package messagepartition 维护消息表的按月分区。
package messagepartition

import (
	"context"
	"time"

	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/uptrace/bun"
)

const (
	EnsureActionName = "message.ensure_partitions"
	ScheduleKey      = "message.partitions"
)

// EnsureInput 定义创建消息分区的输入。
type EnsureInput struct{}

// EnsureAction 提前创建当前及之后月份的消息分区。
type EnsureAction struct{ db *bun.DB }

// NewEnsureAction 创建消息分区维护 Action。
func NewEnsureAction(db *bun.DB) *EnsureAction { return &EnsureAction{db: db} }

// Execute 为当前月份及之后若干月份创建尚不存在的消息分区。
func (a *EnsureAction) Execute(ctx context.Context, _ EnsureInput) error {
	return serverstorage.EnsureMessagePartitions(ctx, a.db, time.Now())
}
