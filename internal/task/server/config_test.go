//go:build server

package server

import "testing"

// TestConfigBuildsIsolatedConsumerNames 验证一个命名空间内的 Worker Pool 使用独立 Consumer 和 Subject。
func TestConfigBuildsIsolatedConsumerNames(t *testing.T) {
	config := runtimeConfig{Namespace: "feature_one"}
	if config.streamName() != "FEATURE_ONE_TASKS" {
		t.Fatalf("stream name = %q", config.streamName())
	}
	if config.consumerName(workerPoolStandard) != "FEATURE_ONE_STANDARD_WORKERS" {
		t.Fatalf("standard consumer name = %q", config.consumerName(workerPoolStandard))
	}
	if config.consumerName(workerPoolAgent) != "FEATURE_ONE_AGENT_WORKERS" {
		t.Fatalf("agent consumer name = %q", config.consumerName(workerPoolAgent))
	}
	if config.filterSubject(workerPoolStandard) != "feature_one.tasks.standard.>" {
		t.Fatalf("standard filter subject = %q", config.filterSubject(workerPoolStandard))
	}
	if config.filterSubject(workerPoolAgent) != "feature_one.tasks.agent.>" {
		t.Fatalf("agent filter subject = %q", config.filterSubject(workerPoolAgent))
	}
}

// TestTaskSubjectRoutesDedicatedQueues 验证专用队列独立消费，未知队列仍使用标准 Worker Pool。
func TestTaskSubjectRoutesDedicatedQueues(t *testing.T) {
	config := runtimeConfig{Namespace: "test_runtime"}
	if subject := config.taskSubject(QueueAgent); subject != "test_runtime.tasks.agent.agent" {
		t.Fatalf("Agent Subject = %q", subject)
	}
	if subject := config.taskSubject(QueueKnowledge); subject != "test_runtime.tasks.knowledge.knowledge" {
		t.Fatalf("knowledge subject=%s", subject)
	}
	if subject := config.taskSubject(QueueDelivery); subject != "test_runtime.tasks.delivery.delivery" {
		t.Fatalf("delivery subject=%s", subject)
	}
	for _, queue := range []string{defaultQueue, "files", "maintenance", "future_queue"} {
		want := "test_runtime.tasks.standard." + queue
		if subject := config.taskSubject(queue); subject != want {
			t.Fatalf("队列 %q Subject = %q，期望 %q", queue, subject, want)
		}
	}
}
