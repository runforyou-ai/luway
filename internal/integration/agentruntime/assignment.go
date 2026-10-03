package agentruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"

	"github.com/runforyou-ai/luway/internal/domain"
)

// AssignmentRulesVersion 是基线与场景规则的规则版本，基线或场景规则增删时加一；措辞调整只体现在指令哈希上。
const AssignmentRulesVersion = 11

// SceneContext 表示拼接场景规则所需的运行期事实，群聊字段只在群聊场景取值，咨询分类只在客服场景取值。
type SceneContext struct {
	Scene             Scene
	GroupTitle        string            // 群聊名称，未命名的群为空。
	MentionCandidates []string          // 群内名称唯一的可点名成员，按名称排序。
	HandoffCategories []HandoffCategory // 企业咨询分类目录，按名称排序。
}

// HandoffCategory 表示转人工时可选的一项咨询分类；模型只看到名称与说明，编号用于交接时复核。
type HandoffCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// AssignmentModel 记录一次运行固定使用的模型编号、参数与输入模态，调用来源在每次模型请求时按模型编号解析。
type AssignmentModel struct {
	ModelID         string                        `json:"modelId"`
	MaxOutputTokens int64                         `json:"maxOutputTokens"`
	ContextWindow   int64                         `json:"contextWindow"`
	InputModalities []domain.AIModelInputModality `json:"inputModalities"`
}

// AssignmentFacts 表示解析有效配置所需的业务事实，由服务端从运行、配置版本与会话读出。
type AssignmentFacts struct {
	HandlesCustomers bool
	OrganizationName string
	AgentName        string
	Instruction      string // 配置版本中的企业指令。
	Model            AssignmentModel
	Scene            SceneContext
}

// Capabilities 表示执行侧本次实际能提供的工具能力。
type Capabilities struct {
	Knowledge     bool     // 本次运行可检索会话绑定的知识库。
	WebSearch     bool     // 企业配置了联网搜索服务。
	WebFetch      bool     // 执行侧可以读取网页。
	MCPServers    []string // 本次运行可连接的企业 MCP 服务名称。
	ComputerTools []string // 运行使用的电脑提供的工具，按电脑工具目录顺序排列。
	// CustomerHistory 表示本次运行关联客户会话，可检索同一客户以往的沟通记录。
	CustomerHistory bool
	// CustomerLoginRequired 表示客服场景中有按客户查询的服务因客户未验证身份而未挂载。
	CustomerLoginRequired bool
	// Memory 表示本次运行属于个人 AI 员工，可以读取其记忆。
	Memory bool
}

// Assignment 是一次运行的有效配置，同时作为运行时入参与运行审计快照。
type Assignment struct {
	HandlesCustomers  bool   `json:"handlesCustomers"`
	AgentName         string `json:"agentName"`
	Scene             Scene  `json:"scene"`
	RulesVersion      int    `json:"rulesVersion"`
	Instruction       string `json:"instruction"`
	InstructionSHA256 string `json:"instructionSha256"`
	// DelegateInstruction 是子 Agent 的运行指令，只在提供委派工具时取值。
	DelegateInstruction string            `json:"delegateInstruction,omitempty"`
	Model               AssignmentModel   `json:"model"`
	Tools               []string          `json:"tools"`
	MCPServers          []string          `json:"mcpServers"`
	Grounding           GroundingPolicy   `json:"grounding,omitempty"`         // 对客正文的依据检查策略，客服场景为严格策略。
	HandoffCategories   []HandoffCategory `json:"handoffCategories,omitempty"` // 转人工时可选的咨询分类，只在客服场景取值。
	Memory              bool              `json:"memory,omitempty"`            // 本次运行注入个人 AI 员工记忆，只在个人 AI 员工与负责人的单聊中取值。
}

// ResolveAssignment 按业务事实与执行侧能力产出一次运行的有效配置；执行侧能力只影响工具清单、指令中的工具说明和 MCP 服务名称。
func ResolveAssignment(facts AssignmentFacts, capabilities Capabilities) Assignment {
	scene := facts.Scene.Scene
	// 联网搜索与网页读取只在内部场景提供，服务场景的回答只以企业资料为依据。
	if scene.Service() {
		capabilities.WebSearch, capabilities.WebFetch = false, false
	}
	tools := builtinTools{
		Knowledge: capabilities.Knowledge, WebSearch: capabilities.WebSearch, WebFetch: capabilities.WebFetch,
		Computer: capabilities.ComputerTools, Orchestration: !scene.Service(), CustomerHistory: capabilities.CustomerHistory, Terminal: scene.Service(),
		HandoffCategories: len(facts.Scene.HandoffCategories) > 0, CustomerLoginRequired: capabilities.CustomerLoginRequired,
	}
	// 员工服务场景使用员工服务台基线，其余场景按接待开关取基线。
	baseline := AgentBaseline(facts.HandlesCustomers, facts.OrganizationName, facts.AgentName)
	if scene == SceneEmployeeService {
		baseline = EmployeeServiceBaseline(facts.OrganizationName, facts.AgentName)
	}
	instruction := composeInstruction(baseline, facts.Instruction, sceneRules(facts.Scene, tools))
	var delegate string
	if tools.Orchestration {
		delegate = delegateInstruction(baseline, facts.Instruction, tools)
	}
	toolNames := builtinToolNames(scene, capabilities)
	sum := sha256.Sum256([]byte(instruction))
	var grounding GroundingPolicy
	if scene.Service() {
		grounding = GroundingStrict
	}
	return Assignment{
		HandlesCustomers:    facts.HandlesCustomers,
		AgentName:           facts.AgentName,
		Scene:               scene,
		RulesVersion:        AssignmentRulesVersion,
		Instruction:         instruction,
		InstructionSHA256:   hex.EncodeToString(sum[:]),
		DelegateInstruction: delegate,
		Model:               facts.Model,
		Tools:               toolNames,
		MCPServers:          mcpServerNames(capabilities),
		Grounding:           grounding,
		HandoffCategories:   facts.Scene.HandoffCategories,
		Memory:              capabilities.Memory && scene == SceneAgentChat,
	}
}

// mcpServerNames 按名称排序复制执行侧提供的 MCP 服务名称，没有服务时输出空数组。
func mcpServerNames(capabilities Capabilities) []string {
	names := make([]string, len(capabilities.MCPServers))
	copy(names, capabilities.MCPServers)
	slices.Sort(names)
	return names
}

// builtinToolNames 按注册顺序列出本次运行的内置工具，任务清单与委派工具只在内部场景注册，电脑工具只在运行使用电脑时注册，客户历史检索只在关联客户会话时注册，终止工具只在服务场景注册。
func builtinToolNames(scene Scene, capabilities Capabilities) []string {
	names := make([]string, 0, 6+len(capabilities.ComputerTools))
	if capabilities.Knowledge {
		names = append(names, KnowledgeToolName)
	}
	if capabilities.WebSearch {
		names = append(names, WebSearchToolName)
	}
	if capabilities.WebFetch {
		names = append(names, WebFetchToolName)
	}
	names = append(names, capabilities.ComputerTools...)
	if !scene.Service() {
		names = append(names, planToolNames...)
		names = append(names, subagentToolName)
	}
	if capabilities.CustomerHistory {
		names = append(names, CustomerHistoryToolName)
	}
	if scene.Service() {
		names = append(names, "ask_customer", "handoff_to_human", "resolve_conversation")
	}
	return names
}
