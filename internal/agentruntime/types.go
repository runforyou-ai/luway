// Package agentruntime 把平台托管 Agent 的有效配置、场景、工具目录与业务能力装配为 einorun 运行，并把运行结果转换为业务结束方式。
package agentruntime

import (
	"context"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/einorun/stream"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/knowledgeretrieval"
	"github.com/runforyou-ai/support/arr"
)

// ModelConfig 定义运行使用的模型参数与模型组件工厂。
type ModelConfig struct {
	MaxOutputTokens int
	ContextWindow   int
	InputModalities []domain.AIModelInputModality
	DisableThinking bool
	New             llm.ModelFactory // 创建对话模型组件，由执行侧注入并负责调用来源与调用记录。
}

// AttachmentContent 读取本次运行会话中指定附件消息的文件内容。
type AttachmentContent func(context.Context, string) ([]byte, error)

// KnowledgeSearch 检索本次 Agent Run 获准使用的知识库。
type KnowledgeSearch func(context.Context, knowledgeretrieval.Request) (knowledgeretrieval.Result, error)

// CustomerHistorySearch 按检索词查询同一客户已关闭的其他客服周期。
type CustomerHistorySearch func(context.Context, string) (agentcontract.CustomerHistoryResult, error)

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

// Dependencies 是执行侧注入的业务依赖，运行按有效配置的工具清单取用；清单列出而未注入依赖的工具仍然注册，调用时返回不可用。
type Dependencies struct {
	KnowledgeSearch       KnowledgeSearch
	WebSearch             WebSearch
	WebFetch              WebFetch
	CustomerHistorySearch CustomerHistorySearch
	BusinessSystems       []agentcontract.BusinessSystem // 本次运行挂载的业务系统，运行结束时关闭其调用会话。
	Computer              Computer                       // 运行使用的电脑。
	// ComputerCapabilities 是电脑在运行开始时上报的执行能力，命令工具说明、本机 MCP 工具与技能目录据此给出。
	ComputerCapabilities domain.ComputerCapabilities
	// ComputerAccess 是运行使用电脑的授权，决定电脑工具执行前需要的人工介入。
	ComputerAccess ComputerAccess
	// SharedFiles 是运行所属会话的共享文件区，为空时文件工具只操作电脑。
	SharedFiles SharedFiles
	Memory      MemoryLoader // 个人 AI 员工的记忆，有效配置启用记忆时读取。
}

// Capabilities 返回依赖能提供的执行侧能力，工作区电脑名称与客户验证由调用方补充。
func (d Dependencies) Capabilities() Capabilities {
	capabilities := Capabilities{
		Knowledge: d.KnowledgeSearch != nil, WebSearch: d.WebSearch != nil, WebFetch: d.WebFetch != nil,
		CustomerHistory: d.CustomerHistorySearch != nil, Memory: d.Memory != nil, SharedFiles: d.SharedFiles != nil,
	}
	capabilities.BusinessSystems = arr.Map(d.BusinessSystems, func(system agentcontract.BusinessSystem) string { return system.Name })
	if d.Computer != nil {
		capabilities.ComputerTools = ComputerToolsFor(d.ComputerCapabilities, d.ComputerAccess)
	}
	return capabilities
}

// RunRequest 定义一次有界 Agent 业务运行。
type RunRequest struct {
	RunID      string
	Assignment Assignment       // 本次运行的有效配置，由 ResolveAssignment 产出并固定在运行快照中。
	Models     llm.ModelFactory // 创建本次运行的对话模型组件，由执行侧注入。
	Dependencies
	ReadAttachment AttachmentContent // 为空时附件只以正文中的链接提供给模型。
	MaxIterations  int               // 单轮模型与工具迭代上限，零值使用默认值。
	MaxTurns       int               // 吸收新输入的轮次上限，零值不限制，由运行 context 控制生命周期。
	StreamID       string
	OnStream       func(stream.Delta) // 串行接收合并后的运行流增量，实现不得阻塞。
	Journal        einorun.Journal    // 在安全点持久化过程与恢复状态，为空时运行只在结束时交回结果。
	Resume         *einorun.Resume    // 非空时从上次保存的恢复状态继续执行。
}

// modelConfig 合并有效配置中的模型参数与执行侧注入的模型组件工厂。
func (r RunRequest) modelConfig() ModelConfig {
	return ModelConfig{
		MaxOutputTokens: int(r.Assignment.Model.MaxOutputTokens),
		ContextWindow:   int(r.Assignment.Model.ContextWindow),
		InputModalities: r.Assignment.Model.InputModalities,
		New:             r.Models,
	}
}

// RunResult 定义稳定 Agent 结果及其输入边界；运行出错或挂起时 Content、Decision 与 EndSeq 为零值，Usage、Blocks、Calls 和 Plan 仍给出已产生的部分。
type RunResult struct {
	Content   string                         // 发给对方的正文：回答、追问内容或结束语；转人工为空。
	Decision  agentcontract.TerminalDecision // 结束方式，Kind 为空表示直接输出正文作为回答。
	EndSeq    int64
	Usage     agentcontract.Usage
	Blocks    []einorun.Block
	Calls     []einorun.ToolCall // 子 Agent 发起的工具调用，主 Agent 的调用在 Blocks 中。
	Plan      []stream.PlanTask  // 运行结束时的任务清单，没有建立清单时为空。
	Suspended bool               // 运行已保存恢复状态并挂起，等待外部结果后继续。
}

// Runtime 执行一次可吸收后续输入的 Agent Run；返回错误时一并给出已产生的用量和内容块。
type Runtime interface {
	Run(context.Context, RunRequest, einorun.Feed) (RunResult, error)
}
