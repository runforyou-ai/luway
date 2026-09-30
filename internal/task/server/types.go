//go:build server

package server

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

const (
	TriggerBusiness = "business"
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
	// QueueAgent 隔离可能长时间运行的 Agent 任务。
	QueueAgent = "agent"
	// QueueKnowledge 隔离文件解析和知识索引任务。
	QueueKnowledge = "knowledge"
	// QueueDelivery 隔离调用外部渠道发送客户消息的任务。
	QueueDelivery = "delivery"
	// QueueEvaluation 隔离 AI 员工评测回放任务，评测不占用在线 Agent 运行的 Worker。
	QueueEvaluation = "evaluation"
)

// EnqueueOptions 定义一次服务端异步 Action 投递参数。
type EnqueueOptions struct {
	Queue          string
	MaxAttempts    int
	IdempotencyKey string
	TriggerType    string
	AvailableAt    time.Time
}

// Enqueuer 将一个 Action 输入提交给服务端任务运行时。
type Enqueuer interface {
	Enqueue(ctx context.Context, actionName string, payload any, options EnqueueOptions) (string, error)
}

// EnqueueRequest 定义批量投递中的一个 Action 输入。
type EnqueueRequest struct {
	ActionName string
	Payload    any
	Options    EnqueueOptions
}

// TxEnqueuer 将 Action 输入加入调用方已经开启的业务事务。
type TxEnqueuer interface {
	EnqueueIn(ctx context.Context, tx bun.IDB, actionName string, payload any, options EnqueueOptions) (string, error)
	EnqueueManyIn(ctx context.Context, tx bun.IDB, requests []EnqueueRequest) ([]string, error)
}

// ScheduleDefinition 定义一个由代码管理的服务端定时 Action。
type ScheduleDefinition struct {
	Key              string
	ActionName       string
	Queue            string
	Payload          any
	CronExpression   string
	Timezone         string
	Enabled          bool
	MaxAttempts      int
	StartImmediately bool
}
