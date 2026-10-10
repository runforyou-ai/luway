//go:build server

// Package server 是服务端任务运行时：经 jetq 在 NATS JetStream 上投递、执行、重试服务端 Action，并由 NATS 服务端触发定时计划。
package server

import (
	"slices"
	"time"

	"github.com/runforyou-ai/jetq"
	"github.com/runforyou-ai/support/arr"
)

const (
	// defaultMaxAttempts 是未指定时任务的最大尝试次数。
	defaultMaxAttempts = 5
	// shutdownGracePeriod 是停止时等待在途任务自然结束的时长。
	shutdownGracePeriod = 5 * time.Second
	// ackWait 是执行中的任务失去续期后由其他服务端实例重新执行前的等待时长。
	ackWait = 2 * time.Minute
	// workspacePausedDelay 是所属工作区未启用时任务推迟执行的时长。
	workspacePausedDelay = 30 * time.Second
	// routeUnheldDelay 是本实例未持有任务路由租约时任务推迟执行的时长。
	routeUnheldDelay = 2 * time.Second
	// routePruneInterval 是删除过期路由租约的间隔。
	routePruneInterval = time.Minute
	// uniqueLockTTL 是带去重键的任务占用该键的最长时长，覆盖最长 24 小时的延迟执行与其后的重试。
	uniqueLockTTL = 48 * time.Hour
	// FailureRetention 是最终失败任务的保留时长，运行概况统计这段时间内的失败任务。
	FailureRetention = 7 * 24 * time.Hour
)

// queues 是本服务端执行的队列及其并发数；投递到其他队列的任务会被拒绝。
var queues = []jetq.Queue{
	{Name: QueueDefault, Concurrency: 4},
	{Name: QueueAgent, Concurrency: 2},
	{Name: QueueKnowledge, Concurrency: 2},
	{Name: QueueDelivery, Concurrency: 4},
	{Name: QueueEvaluation, Concurrency: 2},
	{Name: QueueFiles, Concurrency: 2},
	{Name: QueueMaintenance, Concurrency: 2},
}

// WorkerCount 返回各队列的并发数之和。
func WorkerCount() int {
	return arr.SumBy(queues, func(queue jetq.Queue) int { return queue.Concurrency })
}

// workerQueues 返回带默认重试参数的队列配置。
func workerQueues() []jetq.Queue {
	configured := make([]jetq.Queue, len(queues))
	for index, queue := range queues {
		queue.MaxAttempts = defaultMaxAttempts
		queue.Backoff = jetq.Exponential(15*time.Second, time.Hour)
		queue.AckWait = ackWait
		configured[index] = queue
	}
	return configured
}

// knownQueue 判断队列是否由本服务端执行。
func knownQueue(name string) bool {
	return slices.ContainsFunc(queues, func(queue jetq.Queue) bool { return queue.Name == name })
}
