//go:build server

package server

import (
	"context"
	"time"
)

// 任务队列：每个队列有独立的并发数，长时间运行的任务与其余任务互不占用。
const (
	// QueueDefault 执行未指定队列的业务任务。
	QueueDefault = "default"
	// QueueAgent 隔离可能长时间运行的 Agent 任务。
	QueueAgent = "agent"
	// QueueKnowledge 隔离文件解析和知识索引任务。
	QueueKnowledge = "knowledge"
	// QueueDelivery 隔离调用外部渠道发送客户消息的任务。
	QueueDelivery = "delivery"
	// QueueEvaluation 隔离 AI 员工评测回放任务，评测不占用在线 Agent 运行的 Worker。
	QueueEvaluation = "evaluation"
	// QueueFiles 执行渠道媒体取回与过期文件删除。
	QueueFiles = "files"
	// QueueMaintenance 执行定时维护、授权同步与平台接口凭据刷新。
	QueueMaintenance = "maintenance"
)

// EnqueueOptions 定义一次服务端异步 Action 投递参数：WorkspaceID 是任务所属工作区，平台级任务为空，工作区暂停期间所属任务推迟执行；
// Route 非空时任务只由持有该路由租约的服务端实例执行；IdempotencyKey 在同一 Action 同键任务结束前拒绝重复投递；Delay 是推迟执行的时长。
type EnqueueOptions struct {
	WorkspaceID    string
	Queue          string
	Route          string
	MaxAttempts    int
	IdempotencyKey string
	Delay          time.Duration
}

// Enqueuer 立即投递一个 Action 输入，用于不随业务事务提交的任务。
type Enqueuer interface {
	Enqueue(ctx context.Context, actionName string, payload any, options EnqueueOptions) error
}

// EnqueueRequest 定义批量投递中的一个 Action 输入。
type EnqueueRequest struct {
	ActionName string
	Payload    any
	Options    EnqueueOptions
}

// TxEnqueuer 在当前 serverstorage.RunInTx 写事务内登记 Action 输入，事务提交后投递，回滚时丢弃；返回投递时使用的任务编号。
type TxEnqueuer interface {
	EnqueueIn(ctx context.Context, actionName string, payload any, options EnqueueOptions) (string, error)
	EnqueueManyIn(ctx context.Context, requests []EnqueueRequest) error
}

// DualEnqueuer 同时支持立即投递与随业务事务提交投递。
type DualEnqueuer interface {
	Enqueuer
	TxEnqueuer
}

// Monitor 读取任务概况与最终失败的任务。
type Monitor interface {
	TaskStatus(ctx context.Context) (TaskStatus, error)
	FailedRuns(ctx context.Context, limit, offset int) ([]FailedRun, int, error)
}

// ScheduleDefinition 定义一个由代码管理的定时 Action：CronExpression 为五段 cron 表达式或 @every、@hourly 等描述符，Timezone 为空时按 UTC；
// StartImmediately 为真时服务端启动时另外立即投递一次。
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
