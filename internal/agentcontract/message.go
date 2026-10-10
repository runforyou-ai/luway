// Package agentcontract 定义 Agent 执行引擎与业务侧共用的运行契约：上下文消息、运行结果、过程记录、工具调用结算、业务系统挂载与客户资料。
package agentcontract

// MessageRole 定义模型上下文消息角色。
type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

// Message 定义带持久编号的上下文消息，编号用于跨轮次去重。
type Message struct {
	ID       string
	Revision string // 消息内容的修订标识，同一编号在修订变化后重新进入运行期轮次历史。
	Role     MessageRole
	Content  string
	Media    *Media // 非空表示消息携带附件，模型支持该附件格式时在预算内随消息直传。
	// Fact 表示内容是系统给出的事实（工具调用结果事件），位于最近一条 AI 回复之后时构成回答依据。
	Fact bool
}

// Media 定义上下文消息的附件格式和大小，内容在直传给模型时按消息编号读取。
type Media struct {
	MIMEType string
	ByteSize int64
}
