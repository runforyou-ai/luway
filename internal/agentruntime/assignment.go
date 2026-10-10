package agentruntime

import (
	"slices"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/random"
)

// AssignmentRulesVersion 是基线与场景规则的规则版本，基线或场景规则增删时加一；措辞调整只体现在指令哈希上。
const AssignmentRulesVersion = 13

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
	WorkspaceName    string
	AgentName        string
	Instruction      string // 配置版本中的企业指令。
	Model            AssignmentModel
	Scene            SceneContext
}

// Capabilities 表示执行侧本次实际能提供的工具能力。
type Capabilities struct {
	Knowledge       bool     // 本次运行可检索会话绑定的知识库。
	WebSearch       bool     // 企业配置了联网搜索服务。
	WebFetch        bool     // 执行侧可以读取网页。
	BusinessSystems []string // 本次运行挂载了工具的业务系统名称。
	// ComputerTools 是运行使用的电脑提供的工具，有本机 MCP 工具时含本机 MCP 工具类别，由 ComputerToolsFor 给出。
	ComputerTools []string
	// WorkspaceComputer 是运行使用的工作区电脑名称，使用负责人的个人电脑或不使用电脑时为空。
	WorkspaceComputer string
	// CustomerHistory 表示本次运行关联客户会话，可检索同一客户以往的沟通记录。
	CustomerHistory bool
	// CustomerLoginRequired 表示客服场景中有绑定客户身份的业务系统工具因客户未验证身份而未挂载。
	CustomerLoginRequired bool
	// Memory 表示本次运行属于个人 AI 员工，可以读取其记忆。
	Memory bool
	// SharedFiles 表示本次运行可以读写所属会话的共享文件区。
	SharedFiles bool
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
	DelegateInstruction string          `json:"delegateInstruction,omitempty"`
	Model               AssignmentModel `json:"model"`
	// Tools 是本次运行注册的工具清单，由工具描述表推导：内置工具按名称列出，业务系统与本机 MCP 工具以类别列出，运行时按依赖展开。
	Tools             []string          `json:"tools"`
	BusinessSystems   []string          `json:"businessSystems"`
	Grounding         GroundingPolicy   `json:"grounding,omitempty"`         // 对客正文的依据检查策略，客服场景为严格策略。
	HandoffCategories []HandoffCategory `json:"handoffCategories,omitempty"` // 转人工时可选的咨询分类，只在客服场景取值。
	Memory            bool              `json:"memory,omitempty"`            // 本次运行注入个人 AI 员工记忆，只在个人 AI 员工与负责人的单聊中取值。
}

// ResolveAssignment 按业务事实与执行侧能力产出一次运行的有效配置；执行侧能力只影响工具清单、指令中的工具说明和业务系统名称，工具清单与说明由工具描述表推导。
func ResolveAssignment(facts AssignmentFacts, capabilities Capabilities) Assignment {
	scene := facts.Scene.Scene
	tools := assignmentTools(scene, capabilities)
	g := newGuide(tools, capabilities, facts.Scene)
	// 员工服务场景使用员工服务台基线，其余场景按接待开关取基线。
	baseline := AgentBaseline(facts.HandlesCustomers, facts.WorkspaceName, facts.AgentName)
	if scene == SceneEmployeeService {
		baseline = EmployeeServiceBaseline(facts.WorkspaceName, facts.AgentName)
	}
	instruction := composeInstruction(baseline, facts.Instruction, sceneRules(facts.Scene, g))
	var delegate string
	if g.has(subagentToolName) {
		delegate = delegateInstruction(baseline, facts.Instruction, g)
	}
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
		InstructionSHA256:   random.SHA256Hex([]byte(instruction)),
		DelegateInstruction: delegate,
		Model:               facts.Model,
		Tools:               tools,
		BusinessSystems:     businessSystemNames(capabilities),
		Grounding:           grounding,
		HandoffCategories:   facts.Scene.HandoffCategories,
		Memory:              capabilities.Memory && scene == SceneAgentChat,
	}
}

// businessSystemNames 按名称排序复制执行侧提供的业务系统名称，没有业务系统时输出空数组。
func businessSystemNames(capabilities Capabilities) []string {
	names := make([]string, len(capabilities.BusinessSystems))
	copy(names, capabilities.BusinessSystems)
	slices.Sort(names)
	return names
}
