package agentruntime

import (
	"slices"

	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
)

// SkillToolName 是加载电脑上技能说明的工具名称。
const SkillToolName = "skill"

// ComputerTools 返回每台电脑都提供的工具目录，按工具描述表的顺序排列。
func ComputerTools() []string {
	return arr.OrEmpty(arr.FilterMap(toolSpecs, func(spec toolSpec) (string, bool) {
		return spec.name, spec.computer != nil && spec.computer.core
	}))
}

// ComputerAccess 是运行使用电脑的授权：Grant 为空表示负责人使用自己的个人电脑，全部电脑工具直接执行；Interventions 是本次运行有成员可以处理的人工介入。
type ComputerAccess struct {
	Grant         *domain.ToolGrant
	Interventions []domain.ToolIntervention
}

// policy 按授权给出具有该事实的电脑工具的操作级别与人工介入，授权不允许或需要的人工介入没有成员处理时返回 false。
func (a ComputerAccess) policy(facts domain.ToolFacts) (agentcontract.ToolPolicy, bool) {
	level := domain.ToolLevel(facts, false)
	if a.Grant == nil {
		return agentcontract.ToolPolicy{Level: level}, true
	}
	permitted, intervention := a.Grant.Permit(level)
	if !permitted || intervention != domain.ToolInterventionNone && !slices.Contains(a.Interventions, intervention) {
		return agentcontract.ToolPolicy{}, false
	}
	return agentcontract.ToolPolicy{Level: level, Intervention: intervention}, true
}

// ComputerToolsFor 按电脑上报的执行能力与授权返回运行可用的电脑工具：电脑没有技能时不含技能工具，浏览器与桌面工具随对应能力提供，
// 能力中有本机 Agent 时提供委派工具，授权不允许的工具不含；有授权允许且参数定义可以解析的本机 MCP 工具时末尾附上本机 MCP 工具类别。
func ComputerToolsFor(capabilities domain.ComputerCapabilities, access ComputerAccess) []string {
	names := make([]string, 0)
	for _, spec := range toolSpecs {
		if spec.computer == nil || spec.computer.provided != nil && !spec.computer.provided(capabilities) {
			continue
		}
		if _, ok := access.policy(spec.computer.facts); ok {
			names = append(names, spec.name)
		}
	}
	// 判断是否有可以注册的本机 MCP 工具。
	for _, server := range capabilities.MCPServers {
		for _, item := range server.Tools {
			if _, ok := access.policy(domain.HintedToolFacts(item.ReadOnlyHint, item.DestructiveHint)); !ok {
				continue
			}
			if _, err := mcpParameters(item); err == nil {
				return append(names, MCPToolCategory)
			}
		}
	}
	return names
}

// GrantedComputerTools 返回授权允许的电脑工具目录，授权为空时返回每台电脑都提供的全部工具；localAgents 为真时目录含委派本机 Agent 的工具；需要的人工介入均视为有成员处理。
func GrantedComputerTools(grant *domain.ToolGrant, localAgents bool) []string {
	access := ComputerAccess{Grant: grant, Interventions: []domain.ToolIntervention{domain.ToolInterventionConfirmation, domain.ToolInterventionApproval}}
	names := make([]string, 0)
	for _, spec := range toolSpecs {
		if spec.computer == nil || !spec.computer.core && !(localAgents && spec.name == localAgentToolName) {
			continue
		}
		if _, ok := access.policy(spec.computer.facts); ok {
			names = append(names, spec.name)
		}
	}
	return names
}

// IsComputerTool 判断工具名称是否属于派发到电脑的内置工具。
func IsComputerTool(name string) bool {
	spec, ok := lookupToolSpec(name)
	return ok && spec.computer != nil
}
