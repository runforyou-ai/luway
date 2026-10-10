package agentcontract

import (
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
)

// 工具调用记录中由业务侧写入的注解键，取值写入工具调用表的同名列。
const (
	NoteSource             = "luway.source"               // 工具来源，取 domain.AgentToolSource。
	NoteLevel              = "luway.level"                // 调用时推导的操作级别，取 domain.OperationLevel。
	NoteMCPServer          = "luway.mcp_server"           // 本机 MCP 工具所属的服务名称。
	NoteBusinessSystemID   = "luway.business_system_id"   // 业务系统工具所属的业务系统编号。
	NoteBusinessSystemName = "luway.business_system_name" // 调用时业务系统的名称。
	NoteBoundArguments     = "luway.bound_arguments"      // 业务系统工具由服务端填入的参数，JSON 对象。
	NoteEvidence           = "luway.evidence"             // 原始结果通过依据判定时为 "true"。
)

// 运行媒体引用的键前缀：会话附件以附件消息编号、共享文件区文件以文件区内路径接在前缀之后。
const (
	MediaKeyMessage = "message:"
	MediaKeyShared  = "shared:"
)

// MetaFact 是运行输入消息的元数据键，取值为 "true" 表示消息是系统给出的事实（工具调用结果事件）。
const MetaFact = "luway.fact"

// Submission 是提交确认或审批的调用载荷：需要的人工介入，以及电脑工具调用批准后派发的电脑与操作。
type Submission struct {
	Intervention domain.ToolIntervention `json:"intervention"`
	Target       *ComputerTarget         `json:"target,omitempty"`
}

// submittedResults 是提交确认或审批后交给模型的结果。
var submittedResults = map[domain.ToolIntervention]string{
	domain.ToolInterventionConfirmation: "操作已提交，等待发起人确认后执行；执行结果、拒绝或过期会作为新消息送达。不要重复提交，告知对方需要确认。",
	domain.ToolInterventionApproval:     "操作已提交给负责人审批，批准后执行；执行结果、拒绝或过期会作为新消息送达。不要重复提交，告知对方正在等待审批。",
}

// SubmittedResult 返回提交确认或审批后交给模型的结果。
func SubmittedResult(intervention domain.ToolIntervention) string {
	return submittedResults[intervention]
}

// LocalAgentSubmitted 返回任务交给本机 Agent 后交给模型的结果。
func LocalAgentSubmitted(agent string) string {
	return fmt.Sprintf("任务已交给本机 Agent「%s」，执行过程与最终回复会直接展示给用户。不要复述或总结它的回复，用一句话告诉用户已交给它处理。", agent)
}
