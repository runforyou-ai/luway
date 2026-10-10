//go:build server

package server

// MaintenanceSchedule 返回维护队列中按 UTC 周期触发、启动时立即执行一次、失败不重试的定时计划。
func MaintenanceSchedule(key, actionName, cron string) ScheduleDefinition {
	return ScheduleDefinition{
		Key: key, ActionName: actionName, Queue: QueueMaintenance, Payload: struct{}{}, CronExpression: cron,
		Timezone: "UTC", Enabled: true, MaxAttempts: 1, StartImmediately: true,
	}
}
