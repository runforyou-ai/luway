package domain

// ServiceTimeouts 定义企业客服的未响应提醒、未响应回收、队列等待提醒、AI 超时跟进与 AI 超时关单时长，单位为分钟。
type ServiceTimeouts struct {
	ResponseReminderMinutes int
	ResponseReclaimMinutes  int
	QueueReminderMinutes    int
	AIFollowUpMinutes       int
	AICloseMinutes          int
}
