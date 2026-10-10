package domain

// ServiceReportDimension 定义客服报表的拆分维度。
type ServiceReportDimension string

const (
	ServiceReportDimensionChannel  ServiceReportDimension = "channel"
	ServiceReportDimensionCategory ServiceReportDimension = "category"
)

// ServiceIssueType 定义问题会话的筛选类型。
type ServiceIssueType string

const (
	// ServiceIssueTypeAll 表示满意度为不满意或报表适用的任一质检标记成立。
	ServiceIssueTypeAll ServiceIssueType = "all"
	// ServiceIssueTypeDissatisfied 表示推断满意度为不满意。
	ServiceIssueTypeDissatisfied ServiceIssueType = "dissatisfied"
	// ServiceIssueTypeAIIncorrect 表示 AI 客服答错。
	ServiceIssueTypeAIIncorrect ServiceIssueType = "ai_incorrect"
	// ServiceIssueTypeAIMissedHandoff 表示 AI 客服应转人工未转。
	ServiceIssueTypeAIMissedHandoff ServiceIssueType = "ai_missed_handoff"
	// ServiceIssueTypeAIPoorAttitude 表示 AI 客服态度问题。
	ServiceIssueTypeAIPoorAttitude ServiceIssueType = "ai_poor_attitude"
	// ServiceIssueTypeHumanIncorrect 表示真人客服答错。
	ServiceIssueTypeHumanIncorrect ServiceIssueType = "human_incorrect"
	// ServiceIssueTypeHumanPoorAttitude 表示真人客服态度问题。
	ServiceIssueTypeHumanPoorAttitude ServiceIssueType = "human_poor_attitude"
)
