package domain

// AIModelCallStatus 定义模型调用及其上游尝试的状态。
type AIModelCallStatus string

const (
	AIModelCallStatusRunning   AIModelCallStatus = "running"
	AIModelCallStatusSucceeded AIModelCallStatus = "succeeded"
	AIModelCallStatusFailed    AIModelCallStatus = "failed"
	AIModelCallStatusCanceled  AIModelCallStatus = "canceled"
	AIModelCallStatusTimedOut  AIModelCallStatus = "timed_out"
)

// AIModelCallActor 定义模型调用的发起主体类型。
type AIModelCallActor string

const (
	AIModelCallActorMember AIModelCallActor = "member"
	AIModelCallActorAgent  AIModelCallActor = "agent"
	AIModelCallActorSystem AIModelCallActor = "system"
)

// AIModelCallSource 定义模型调用所服务的业务对象类型。
type AIModelCallSource string

const (
	AIModelCallSourceAgentRun          AIModelCallSource = "agent_run"
	AIModelCallSourceAgentEvaluation   AIModelCallSource = "agent_evaluation"
	AIModelCallSourceConversation      AIModelCallSource = "conversation"
	AIModelCallSourceServiceSession    AIModelCallSource = "service_session"
	AIModelCallSourceKnowledgeBase     AIModelCallSource = "knowledge_base"
	AIModelCallSourceKnowledgeDocument AIModelCallSource = "knowledge_document"
	AIModelCallSourceKnowledgeQAEntry  AIModelCallSource = "knowledge_qa_entry"
	AIModelCallSourceChannel           AIModelCallSource = "channel"
)
