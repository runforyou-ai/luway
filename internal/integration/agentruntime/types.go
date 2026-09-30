// Package agentruntime 使用 Eino 执行平台托管 Agent。
package agentruntime

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/localskill"

	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/luway/internal/integration/knowledgeretrieval"
)

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
}

// Media 定义上下文消息的附件格式和大小，内容在直传给模型时按消息编号读取。
type Media struct {
	MIMEType string
	ByteSize int64
}

// Trigger 定义等待 TurnLoop 消费的输入信号。
type Trigger struct {
	Seq        int64
	Correction bool // 为 true 表示 Runtime 发起的依据纠正重新执行，不认领持久输入。
}

// ClaimedInput 定义一次 GenInput 已持久化认领的模型输入。
type ClaimedInput struct {
	Messages []Message
	EndSeq   int64
}

// InputFeed 提供 Agent Run 的持久化输入流。
type InputFeed interface {
	Peek(context.Context, int64) ([]Trigger, error)
	Claim(context.Context, int64) (ClaimedInput, error)
}

// ModelConfig 定义模型组件运行所需配置。
type ModelConfig struct {
	Brand           string
	APIKey          string
	BaseURL         string
	Identifier      string
	MaxOutputTokens int
	ContextWindow   int
	InputModalities []domain.AIModelInputModality
	DisableThinking bool              // 为 true 时在模型组件提供思考开关的品牌上关闭思考模式。
	Transport       http.RoundTripper // 非空时模型请求经该传输层发出。
}

// AttachmentContent 读取本次运行会话中指定附件消息的文件内容。
type AttachmentContent func(context.Context, string) ([]byte, error)

// KnowledgeSearch 检索本次 Agent Run 获准使用的知识库。
type KnowledgeSearch func(context.Context, knowledgeretrieval.Request) (knowledgeretrieval.Result, error)

// CustomerHistorySearch 按检索词查询同一客户已关闭的其他客服周期。
type CustomerHistorySearch func(context.Context, string) (CustomerHistoryResult, error)

// CustomerHistoryResult 按相关度从高到低列出命中的过往咨询，没有命中时 Message 说明结果。
type CustomerHistoryResult struct {
	Sessions []CustomerHistorySession `json:"sessions"`
	Message  string                   `json:"message,omitempty"`
}

// CustomerHistorySession 是命中的一次过往咨询：小结与命中消息前后的对客消息。
type CustomerHistorySession struct {
	CustomerHistorySummary
	Messages []CustomerHistoryMessage `json:"messages"`
}

// CustomerHistoryMessage 是过往咨询中的一条对客消息，Sender 为 customer、agent 或 member，Attachment 是附件消息的文件名。
type CustomerHistoryMessage struct {
	Sender     string    `json:"sender"`
	Body       string    `json:"body"`
	Attachment string    `json:"attachment,omitempty"`
	SentAt     time.Time `json:"sentAt"`
}

// Scene 表示一次运行所属的业务场景。
type Scene string

const (
	SceneCustomer        Scene = "customer"
	SceneEmployeeService Scene = "employee_service"
	SceneAgentChat       Scene = "agent_chat"
	SceneGroup           Scene = "group"
	SceneCopilot         Scene = "copilot"
)

// Service 判断场景是否由 AI 员工首接待服务请求：输出直接发给服务对象，可追问、转人工和结束服务。
func (s Scene) Service() bool {
	return s == SceneCustomer || s == SceneEmployeeService
}

// GroundingPolicy 表示对客正文的依据检查策略。
type GroundingPolicy string

// GroundingStrict 要求直接输出的正文在当前输入边界内取得有效依据，否则纠正一次后转人工。
const GroundingStrict GroundingPolicy = "strict"

// ModelCredentials 定义调用模型所需的凭据与入口，由执行侧注入，不进入有效配置；设备执行时指向服务端模型代理。
type ModelCredentials struct {
	APIKey    string
	BaseURL   string
	Transport http.RoundTripper // 非空时模型请求经该传输层发出。
}

// Workspace 是执行设备提供的本机文件与命令访问，相对路径与命令工作目录以会话默认文件夹为起点。
type Workspace interface {
	filesystem.Backend
	filesystem.Shell
	// Delete 删除一个文件或空文件夹。
	Delete(ctx context.Context, path string) error
}

// LocalMCPServer 是助理为这台电脑添加的本地 MCP 服务：Type 为空或 stdio 时启动本地进程，为 sse 或 http 时连接服务地址。
type LocalMCPServer struct {
	Name    string
	Type    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
}

// LocalMCP 是执行设备提供的本地 MCP 服务管理，这台电脑上主人的所有助理共用。
type LocalMCP interface {
	// Add 试启动服务并读取工具目录，成功后保存配置并返回服务提供的工具名称；同名服务被替换。
	Add(ctx context.Context, server LocalMCPServer) ([]string, error)
	// Remove 删除服务配置，返回服务是否存在。
	Remove(ctx context.Context, name string) (bool, error)
	// Names 按名称顺序返回已添加的服务。
	Names(ctx context.Context) ([]string, error)
}

// LocalSkills 是执行设备提供的技能目录，这台电脑上主人的所有助理共用。
type LocalSkills interface {
	// List 按名称顺序返回可用技能。
	List(ctx context.Context) ([]localskill.Skill, error)
	// Load 返回技能及其 SKILL.md 正文。
	Load(ctx context.Context, name string) (localskill.Skill, string, error)
	// Install 从来源安装技能，来源含多个技能时按 name 选择；同名技能被替换。
	Install(ctx context.Context, source, name string) (localskill.Skill, error)
	// Remove 删除助理安装的技能，返回技能是否存在。
	Remove(ctx context.Context, name string) (bool, error)
}

// RunRequest 定义一次有界 Agent 业务运行。
type RunRequest struct {
	RunID                 string
	Assignment            Assignment       // 本次运行的有效配置，由 ResolveAssignment 产出并固定在运行快照中。
	Credentials           ModelCredentials // 模型供应商凭据。
	KnowledgeSearch       KnowledgeSearch
	WebSearch             WebSearch // 有效配置包含联网搜索时由执行侧提供。
	WebFetch              WebFetch  // 有效配置包含网页读取时由执行侧提供。
	CustomerHistorySearch CustomerHistorySearch
	ReadAttachment        AttachmentContent // 为空时附件只以正文中的链接提供给模型。
	MCPConnections        []MCPServer       // 有效配置中远程 MCP 服务对应的连接配置。
	Workspace             Workspace         // 有效配置包含本机工具时由执行设备提供的本机文件与命令访问。
	LocalMCP              LocalMCP          // 有效配置包含本地 MCP 管理工具时由执行设备提供。
	Skills                LocalSkills       // 有效配置包含技能工具时由执行设备提供。
	Memory                MemoryLoader      // 有效配置启用记忆时由执行侧提供。
	ManagedToolchain      bool              // 执行设备为命令提供了托管的 uv、Node.js 与 Python，命令工具与本地 MCP 工具的说明随之补充用法。
	MaxIterations         int               // 单轮模型与工具迭代上限，零值使用默认值。
	MaxTurns              int               // 吸收新输入的轮次上限，零值不限制，由运行 context 控制生命周期。
	StreamID              string
	Attempt               int
	OnStream              func(runstream.Delta) // 串行接收合并后的运行流增量，实现不得阻塞。
}

// modelConfig 合并有效配置中的模型参数与执行侧注入的凭据。
func (r RunRequest) modelConfig() ModelConfig {
	return ModelConfig{
		Brand:           r.Assignment.Model.Brand,
		APIKey:          r.Credentials.APIKey,
		BaseURL:         r.Credentials.BaseURL,
		Identifier:      r.Assignment.Model.Identifier,
		MaxOutputTokens: int(r.Assignment.Model.MaxOutputTokens),
		ContextWindow:   int(r.Assignment.Model.ContextWindow),
		InputModalities: r.Assignment.Model.InputModalities,
		Transport:       r.Credentials.Transport,
	}
}

// Usage 定义一次业务运行累计的模型用量。
type Usage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// add 累计一次模型输出的用量，输出未携带用量时不变。
func (u *Usage) add(meta *schema.AgenticResponseMeta) {
	if meta == nil || meta.TokenUsage == nil {
		return
	}
	u.PromptTokens += meta.TokenUsage.PromptTokens
	u.CompletionTokens += meta.TokenUsage.CompletionTokens
	u.TotalTokens += meta.TokenUsage.TotalTokens
}

// merge 累计另一份用量。
func (u *Usage) merge(other Usage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
}

// RunResult 定义稳定 Agent 结果及其输入边界；运行出错时 Content、Decision 与 EndSeq 为零值，Usage、Blocks 和 Plan 仍给出已产生的部分。
type RunResult struct {
	Content  string           // 发给对方的正文：回答、追问内容或转人工说明；Runtime 构造的转人工为空。
	Decision TerminalDecision // 结束方式，Kind 为空表示直接输出正文作为回答。
	EndSeq   int64
	Usage    Usage
	Blocks   []Block
	Plan     []runstream.PlanTask // 运行结束时的任务清单，没有建立清单时为空。
}

// Runtime 执行一次可吸收后续输入的 Agent Run；返回错误时一并给出已产生的用量和内容块。
type Runtime interface {
	Run(context.Context, RunRequest, InputFeed) (RunResult, error)
}
