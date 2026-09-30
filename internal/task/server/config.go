//go:build server

// Package server 实现基于 PostgreSQL 和 NATS JetStream 的服务端任务运行时。
package server

import (
	"strings"
	"time"

	serverconfig "github.com/runforyou-ai/cervi/internal/config/server"
)

const (
	natsStartupTimeout    = 15 * time.Second
	shutdownGracePeriod   = 5 * time.Second
	taskStreamMaxBytes    = int64(1 << 30)
	taskStreamMaxAge      = 30 * 24 * time.Hour
	taskReplicas          = 1
	standardTaskWorkers   = 4
	agentTaskWorkers      = 2
	knowledgeTaskWorkers  = 2
	deliveryTaskWorkers   = 4
	evaluationTaskWorkers = 2
	taskPoolMaxAckPending = 1024
	workerPoolStandard    = "standard"
	workerPoolAgent       = "agent"
	workerPoolKnowledge   = "knowledge"
	workerPoolDelivery    = "delivery"
	workerPoolEvaluation  = "evaluation"
)

// workerPoolConfig 定义一组相互隔离的任务 Worker。
type workerPoolConfig struct {
	Name          string
	Workers       int
	MaxAckPending int
}

// runtimeConfig 定义服务端任务运行时配置。
type runtimeConfig struct {
	URL            string
	Namespace      string
	StartupTimeout time.Duration
	// ShutdownGracePeriod 是停止时等待在途任务自然结束的时长，超时后取消并退回任务。
	ShutdownGracePeriod time.Duration
	MaxBytes            int64
	MaxAge              time.Duration
	Replicas            int
	WorkerPools         []workerPoolConfig
}

// newConfig 补充任务运行时的固定配置。
func newConfig(nats serverconfig.NATSConfig) runtimeConfig {
	return runtimeConfig{
		URL:                 nats.URL,
		Namespace:           nats.Namespace,
		StartupTimeout:      natsStartupTimeout,
		ShutdownGracePeriod: shutdownGracePeriod,
		MaxBytes:            taskStreamMaxBytes,
		MaxAge:              taskStreamMaxAge,
		Replicas:            taskReplicas,
		WorkerPools: []workerPoolConfig{
			{Name: workerPoolStandard, Workers: standardTaskWorkers, MaxAckPending: taskPoolMaxAckPending},
			{Name: workerPoolAgent, Workers: agentTaskWorkers, MaxAckPending: taskPoolMaxAckPending},
			{Name: workerPoolKnowledge, Workers: knowledgeTaskWorkers, MaxAckPending: taskPoolMaxAckPending},
			{Name: workerPoolDelivery, Workers: deliveryTaskWorkers, MaxAckPending: taskPoolMaxAckPending},
			{Name: workerPoolEvaluation, Workers: evaluationTaskWorkers, MaxAckPending: taskPoolMaxAckPending},
		},
	}
}

// streamName 生成任务 Stream 名称。
func (c runtimeConfig) streamName() string {
	return strings.ToUpper(c.Namespace) + "_TASKS"
}

// consumerName 生成指定 Worker Pool 的 Consumer 名称。
func (c runtimeConfig) consumerName(pool string) string {
	return strings.ToUpper(c.Namespace) + "_" + strings.ToUpper(pool) + "_WORKERS"
}

// subjectPrefix 生成任务 Subject 前缀。
func (c runtimeConfig) subjectPrefix() string {
	return c.Namespace + ".tasks"
}

// filterSubject 生成指定 Worker Pool 的订阅过滤条件。
func (c runtimeConfig) filterSubject(pool string) string {
	return c.subjectPrefix() + "." + pool + ".>"
}

// taskSubject 生成指定逻辑队列的发布 Subject。
func (c runtimeConfig) taskSubject(queue string) string {
	// 按逻辑队列选择 Worker Pool。
	pool := workerPoolStandard
	switch queue {
	case QueueKnowledge:
		pool = workerPoolKnowledge
	case QueueAgent:
		pool = workerPoolAgent
	case QueueDelivery:
		pool = workerPoolDelivery
	case QueueEvaluation:
		pool = workerPoolEvaluation
	}
	return c.subjectPrefix() + "." + pool + "." + queue
}
