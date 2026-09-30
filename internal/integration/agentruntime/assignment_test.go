package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
)

// customerFacts 构造一份客服场景的业务事实。
func customerFacts() AssignmentFacts {
	return AssignmentFacts{
		HandlesCustomers: true, OrganizationName: "演示公司", AgentName: "小鹿", Instruction: "只回答售后问题。",
		Model: AssignmentModel{
			ProviderID: "provider", Brand: "deepseek", Identifier: "model", MaxOutputTokens: 1024, ContextWindow: 8192,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText},
		},
		Scene: SceneContext{Scene: SceneCustomer},
	}
}

// TestComposeInstruction 验证基线、企业指令与场景规则的拼接顺序和空段落处理。
func TestComposeInstruction(t *testing.T) {
	baseline := AgentBaseline(true, "演示公司", "小鹿")
	if !strings.HasPrefix(baseline, "你是企业「演示公司」的 AI 员工「小鹿」，专业领域是客户服务。") {
		t.Fatalf("客服基线 = %q", baseline)
	}
	if generic := AgentBaseline(true, "演示公司", ""); !strings.HasPrefix(generic, "你是企业「演示公司」的 AI 员工，专业领域是客户服务。") {
		t.Fatalf("省略名称的基线 = %q", generic)
	}
	if member := AgentBaseline(false, "演示公司", "小鹿"); !strings.HasPrefix(member, "你是企业「演示公司」的 AI 员工「小鹿」，协助企业同事工作。") {
		t.Fatalf("未开启接待的基线 = %q", member)
	}
	full := composeInstruction(baseline, "  只回答售后问题。 ", agentChatSceneRules)
	if !strings.HasPrefix(full, baseline+"\n\n只回答售后问题。\n\n"+agentChatSceneRules) {
		t.Fatalf("拼接结果 = %q", full)
	}
	if noEnterprise := composeInstruction(baseline, "   ", agentChatSceneRules); noEnterprise != baseline+"\n\n"+agentChatSceneRules {
		t.Fatalf("空企业指令不应占位：%q", noEnterprise)
	}
}

// TestToolGuidance 验证场景规则只描述本次运行实际注册的工具。
func TestToolGuidance(t *testing.T) {
	if guidance := toolGuidance(builtinTools{}); guidance != "" {
		t.Fatalf("没有内置工具时不应生成说明：%q", guidance)
	}
	guidance := toolGuidance(builtinTools{Knowledge: true, CustomerHistory: true})
	if !strings.HasPrefix(guidance, "可用工具：\n") || !strings.Contains(guidance, "search_knowledge") || !strings.Contains(guidance, "search_customer_history") {
		t.Fatalf("工具说明 = %q", guidance)
	}
	if only := toolGuidance(builtinTools{Knowledge: true}); strings.Contains(only, "search_customer_history") {
		t.Fatalf("未注册的工具不应出现：%q", only)
	}
	if only := toolGuidance(builtinTools{Knowledge: true}); strings.Contains(only, "ask_customer") || strings.Contains(only, "handoff_to_human") {
		t.Fatalf("内部场景不应出现终止工具：%q", only)
	}
	terminal := toolGuidance(builtinTools{Terminal: true})
	if !strings.HasPrefix(terminal, "可用工具：\n") || !strings.Contains(terminal, "- ask_customer：") || !strings.Contains(terminal, "- handoff_to_human：") {
		t.Fatalf("终止工具说明 = %q", terminal)
	}
	// 企业没有咨询分类时不提及分类，有分类时说明只在明确匹配时填写。
	if strings.Contains(terminal, "分类") {
		t.Fatalf("没有咨询分类时不应提及分类：%q", terminal)
	}
	if categories := toolGuidance(builtinTools{Terminal: true, HandoffCategories: true}); !strings.Contains(categories, "没有明确匹配的分类时不填") {
		t.Fatalf("咨询分类说明 = %q", categories)
	}
}

// TestSceneRules 验证群聊场景写明群名并列出可点名成员，未命名的群不写群名，没有可点名成员时明确告知。
func TestSceneRules(t *testing.T) {
	rules := sceneRules(SceneContext{Scene: SceneGroup, GroupTitle: "售后组", MentionCandidates: []string{"小鹿", "老王"}}, builtinTools{})
	if !strings.Contains(rules, "群聊「售后组」") || !strings.Contains(rules, "小鹿、老王") {
		t.Fatalf("群聊场景规则 = %q", rules)
	}
	if empty := sceneRules(SceneContext{Scene: SceneGroup}, builtinTools{}); !strings.Contains(empty, "本次在群聊中") || !strings.Contains(empty, "可点名的成员：无") {
		t.Fatalf("无可点名成员时的场景规则 = %q", empty)
	}
	// 按客户查询的服务因客户未登录而未挂载时才说明引导登录。
	if customer := sceneRules(SceneContext{Scene: SceneCustomer}, builtinTools{Terminal: true}); strings.Contains(customer, customerLoginRequiredRule) {
		t.Fatalf("客户已登录时的场景规则 = %q", customer)
	}
	login := ResolveAssignment(customerFacts(), Capabilities{CustomerLoginRequired: true})
	if !strings.Contains(login.Instruction, customerLoginRequiredRule) {
		t.Fatalf("客户未登录时的指令 = %q", login.Instruction)
	}
}

// TestResolveAssignment 验证有效配置记录接待开关、场景、规则版本、完整指令、哈希与工具清单。
func TestResolveAssignment(t *testing.T) {
	assignment := ResolveAssignment(customerFacts(), Capabilities{Knowledge: true, MCPServers: []string{"工单系统"}})
	if !assignment.HandlesCustomers || assignment.Scene != SceneCustomer ||
		assignment.RulesVersion != AssignmentRulesVersion || assignment.Grounding != GroundingStrict {
		t.Fatalf("有效配置 = %+v", assignment)
	}
	if !strings.Contains(assignment.Instruction, "只回答售后问题。") || !strings.HasSuffix(assignment.Instruction, customerFollowUpRule) {
		t.Fatalf("有效配置指令 = %q", assignment.Instruction)
	}
	if len(assignment.InstructionSHA256) != 64 || assignment.AgentName != "小鹿" || assignment.Model.Brand != "deepseek" ||
		assignment.Model.ProviderID != "provider" || assignment.Model.ContextWindow != 8192 ||
		len(assignment.Model.InputModalities) != 1 || len(assignment.MCPServers) != 1 {
		t.Fatalf("有效配置元数据 = %+v", assignment)
	}
	// 客服场景注册知识检索与终止工具，不注册开发期计算器。
	if strings.Join(assignment.Tools, ",") != "search_knowledge,ask_customer,handoff_to_human,resolve_conversation" {
		t.Fatalf("客服工具清单 = %v", assignment.Tools)
	}
	// 关联客户会话时注册客户历史检索并在指令中说明。
	history := ResolveAssignment(customerFacts(), Capabilities{Knowledge: true, CustomerHistory: true})
	if strings.Join(history.Tools, ",") != "search_knowledge,search_customer_history,ask_customer,handoff_to_human,resolve_conversation" ||
		!strings.Contains(history.Instruction, customerSceneHistoryToolGuidance) || strings.Contains(history.Instruction, customerHistoryToolGuidance) {
		t.Fatalf("客户历史有效配置 = %+v", history)
	}
	internal := ResolveAssignment(AssignmentFacts{Scene: SceneContext{Scene: SceneAgentChat}}, Capabilities{})
	if strings.Join(internal.Tools, ",") != "calculator,TaskCreate,TaskGet,TaskUpdate,TaskList,agent" || internal.Grounding != "" {
		t.Fatalf("内部场景有效配置 = %+v", internal)
	}
	if internal.InstructionSHA256 == assignment.InstructionSHA256 {
		t.Fatal("不同指令应产生不同哈希")
	}
}

// TestResolveAssignmentDeterministic 验证同一份业务事实与执行侧能力在任意执行位置解析出逐字节相同的有效配置。
func TestResolveAssignmentDeterministic(t *testing.T) {
	facts, capabilities := customerFacts(), Capabilities{Knowledge: true, MCPServers: []string{"工单系统"}}
	first, err := json.Marshal(ResolveAssignment(facts, capabilities))
	if err != nil {
		t.Fatalf("序列化有效配置：%v", err)
	}
	second, err := json.Marshal(ResolveAssignment(facts, capabilities))
	if err != nil {
		t.Fatalf("序列化有效配置：%v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("同一份事实解析结果不一致：\n%s\n%s", first, second)
	}
	// 执行侧能力不同时只允许工具清单及其在指令中的说明随之变化。
	without := ResolveAssignment(facts, Capabilities{MCPServers: capabilities.MCPServers})
	with := ResolveAssignment(facts, capabilities)
	without.Tools, without.Instruction, without.InstructionSHA256 = with.Tools, with.Instruction, with.InstructionSHA256
	aligned, err := json.Marshal(without)
	if err != nil {
		t.Fatalf("序列化有效配置：%v", err)
	}
	expected, err := json.Marshal(with)
	if err != nil {
		t.Fatalf("序列化有效配置：%v", err)
	}
	if string(aligned) != string(expected) {
		t.Fatalf("能力差异影响了工具清单以外的配置：\n%s\n%s", aligned, expected)
	}
}

// TestResolveAssignmentNormalizesMCPServers 验证 MCP 服务名称由解析器按名称排序并收敛为数组，执行侧的传入顺序与空值不影响结果。
func TestResolveAssignmentNormalizesMCPServers(t *testing.T) {
	facts := customerFacts()
	if servers := ResolveAssignment(facts, Capabilities{MCPServers: []string{"ticket", "billing"}}).MCPServers; strings.Join(servers, ",") != "billing,ticket" {
		t.Fatalf("mcp 服务名称 = %v", servers)
	}
	fromNil, err := json.Marshal(ResolveAssignment(facts, Capabilities{}))
	if err != nil {
		t.Fatalf("序列化有效配置：%v", err)
	}
	fromEmpty, err := json.Marshal(ResolveAssignment(facts, Capabilities{MCPServers: []string{}}))
	if err != nil {
		t.Fatalf("序列化有效配置：%v", err)
	}
	if string(fromNil) != string(fromEmpty) || !strings.Contains(string(fromNil), `"mcpServers":[]`) {
		t.Fatalf("没有 MCP 服务时的快照：\n%s\n%s", fromNil, fromEmpty)
	}
}

// TestResolveAssignmentLocalAgent 验证本机 Agent 执行时不注册应用工具、不带模型，指令保留企业指令与群聊点名规则。
func TestResolveAssignmentLocalAgent(t *testing.T) {
	facts := customerFacts()
	facts.HandlesCustomers, facts.Instruction, facts.LocalAgent = false, "整理周报。", domain.LocalAgentKindCodex
	facts.Scene = SceneContext{Scene: SceneGroup, MentionCandidates: []string{"张三"}}
	assignment := ResolveAssignment(facts, Capabilities{Knowledge: true, WebFetch: true, LocalTools: LocalTools(), Memory: true, MCPServers: []string{"crm"}})
	if assignment.LocalAgent != domain.LocalAgentKindCodex || len(assignment.Tools) != 0 || len(assignment.MCPServers) != 0 ||
		assignment.Model.Identifier != "" || assignment.Memory || assignment.DelegateInstruction != "" {
		t.Fatalf("ResolveAssignment() = %+v", assignment)
	}
	if !strings.Contains(assignment.Instruction, "整理周报。") || !strings.Contains(assignment.Instruction, "张三") || strings.Contains(assignment.Instruction, "search_knowledge") {
		t.Fatalf("instruction = %q", assignment.Instruction)
	}
}
