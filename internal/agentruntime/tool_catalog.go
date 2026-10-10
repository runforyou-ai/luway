package agentruntime

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
)

const (
	// BusinessSystemToolCategory 是工具清单中代表业务系统工具的类别，业务系统工具按运行挂载的工具目录注册。
	BusinessSystemToolCategory = string(domain.AgentToolSourceBusinessSystem)
	// MCPToolCategory 是工具清单中代表电脑本机 MCP 工具的类别，本机 MCP 工具按电脑上报的服务与授权注册。
	MCPToolCategory = string(domain.AgentToolSourceMCP)
)

// errToolUnavailable 是清单列出而本次运行缺少依赖的工具被调用时交给模型的失败原因。
var errToolUnavailable = errors.New("这个工具当前不可用，调用没有执行。请改用其他方式完成，或告诉对方暂时无法处理。")

// toolSpec 描述一个内置工具或工具类别：清单列入条件、特性、依据判定、提示说明与构造方式。
type toolSpec struct {
	name     string
	traits   toolTraits    // 来源、中断后能否重新执行与是否产生外部副作用。
	computer *computerSpec // 派发到电脑的工具，按授权取得操作级别与执行前需要的人工介入。
	evidence evidenceJudge // 严格依据策略下判定结果是否构成回答依据，为空表示不是依据来源。
	writes   bool          // 结果构成依据但会写入数据，纠正提示不要求调用它查证。
	perAgent bool          // 每个 Agent 各自构建，子 Agent 不沿用主 Agent 的实例。
	mainOnly bool          // 只注册给主 Agent。
	category bool          // 代表运行时按依赖展开的一组工具。
	// offered 按场景与执行侧能力判断有效配置是否列入。
	offered func(scene Scene, capabilities Capabilities) bool
	// guidance 返回场景规则中的工具说明，只对清单列出的条目调用，不需要说明时返回空串。
	guidance func(g guide) string
	// build 按运行依赖构造工具；为空表示工具由运行时或内置扩展注册，或属于工具类别。
	build func(b *toolBuild, spec *toolSpec) (toolEntry, error)
}

// computerSpec 描述派发到电脑的工具：事实决定操作级别，core 表示每台电脑都提供，provided 按电脑上报的能力判断是否提供，create 创建工具。
type computerSpec struct {
	facts     domain.ToolFacts
	core      bool
	provided  func(domain.ComputerCapabilities) bool
	sequenced bool // 浏览器与桌面工具按模型给出的顺序执行。
	create    func(b *toolBuild) (*computerTool, error)
}

// readOnlyTraits 是没有外部副作用、可重新执行的内置工具特性。
var readOnlyTraits = toolTraits{source: domain.AgentToolSourceBuiltin, replayable: true}

// sideEffectTraits 是修改电脑或外部状态的内置工具特性，replayable 表示重复执行得到相同结果。
func sideEffectTraits(replayable bool) toolTraits {
	return toolTraits{source: domain.AgentToolSourceBuiltin, replayable: replayable, sideEffects: true}
}

// internalScene 判断场景不是服务场景，任务清单、委派、联网搜索与网页读取只在内部场景提供。
func internalScene(scene Scene, _ Capabilities) bool { return !scene.Service() }

// serviceScene 判断场景是服务场景，终止工具只在服务场景提供。
func serviceScene(scene Scene, _ Capabilities) bool { return scene.Service() }

// offeredByComputer 判断工具在执行侧提供的电脑工具中。
func offeredByComputer(name string) func(Scene, Capabilities) bool {
	return func(_ Scene, capabilities Capabilities) bool {
		return slices.Contains(capabilities.ComputerTools, name)
	}
}

// offeredFileTool 判断文件工具由电脑或会话共享文件区提供。
func offeredFileTool(name string) func(Scene, Capabilities) bool {
	return func(_ Scene, capabilities Capabilities) bool {
		return capabilities.SharedFiles || slices.Contains(capabilities.ComputerTools, name)
	}
}

// fixedGuidance 返回固定的工具说明。
func fixedGuidance(text string) func(guide) string {
	return func(guide) string { return text }
}

// toolSpecs 是全部内置工具与工具类别，按清单与提示说明的顺序排列。
var toolSpecs = []toolSpec{
	{
		name: KnowledgeToolName, traits: readOnlyTraits, evidence: knowledgeEvidence,
		offered:  func(_ Scene, c Capabilities) bool { return c.Knowledge },
		guidance: fixedGuidance(knowledgeToolGuidance),
		build: func(b *toolBuild, spec *toolSpec) (toolEntry, error) {
			created, err := newKnowledgeSearchTool(b.request.KnowledgeSearch)
			return spec.entry(created), err
		},
	},
	{
		name: WebSearchToolName, traits: readOnlyTraits,
		offered:  func(s Scene, c Capabilities) bool { return !s.Service() && c.WebSearch },
		guidance: fixedGuidance(webSearchToolGuidance),
		build: func(b *toolBuild, spec *toolSpec) (toolEntry, error) {
			return spec.entry(newWebSearchTool(b.request.WebSearch)), nil
		},
	},
	{
		name: WebFetchToolName, traits: readOnlyTraits,
		offered:  func(s Scene, c Capabilities) bool { return !s.Service() && c.WebFetch },
		guidance: fixedGuidance(webFetchToolGuidance),
		build: func(b *toolBuild, spec *toolSpec) (toolEntry, error) {
			return spec.entry(newWebFetchTool(b.request.WebFetch)), nil
		},
	},
	{
		name: readFileToolName, traits: readOnlyTraits, evidence: queryEvidence, perAgent: true,
		computer: &computerSpec{facts: domain.ToolFacts{ReadOnly: true}, core: true, create: func(*toolBuild) (*computerTool, error) {
			return newComputerTool(readFileToolName, readFileToolDesc, func(input readFileArgs) (domain.ComputerOperation, error) {
				return domain.ComputerOperation{Kind: domain.ComputerOperationReadFile, Path: input.FilePath, Offset: input.Offset, Limit: input.Limit}, nil
			})
		}},
		offered: offeredFileTool(readFileToolName), build: buildFileTool,
		// 电脑上的文件与命令工具合并说明，文件工具只读写共享文件区时不说明。
		guidance: func(g guide) string {
			if len(g.computer) == 0 {
				return ""
			}
			return fmt.Sprintf(computerToolGuidance, strings.Join(g.computer, "、"), g.location, g.offline)
		},
	},
	{
		name: writeFileToolName, traits: sideEffectTraits(true), evidence: queryEvidence, writes: true, perAgent: true,
		// 覆盖已有文件时以读取或写入后的内容摘要作为条件。
		computer: &computerSpec{facts: domain.ToolFacts{Reversible: true}, core: true, create: func(b *toolBuild) (*computerTool, error) {
			return newComputerTool(writeFileToolName, writeFileToolDesc, func(input writeFileArgs) (domain.ComputerOperation, error) {
				return domain.ComputerOperation{Kind: domain.ComputerOperationWriteFile, Path: input.FilePath, Content: input.Content, BaseHash: b.versions.base(input.FilePath)}, nil
			})
		}},
		offered: offeredFileTool(writeFileToolName), build: buildFileTool,
	},
	{
		name: editFileToolName, traits: sideEffectTraits(false), evidence: queryEvidence, writes: true, perAgent: true,
		// 修改时以读取或写入后的内容摘要作为条件。
		computer: &computerSpec{facts: domain.ToolFacts{Reversible: true}, core: true, create: func(b *toolBuild) (*computerTool, error) {
			return newComputerTool(editFileToolName, editFileToolDesc, func(input editFileArgs) (domain.ComputerOperation, error) {
				return domain.ComputerOperation{
					Kind: domain.ComputerOperationEditFile, Path: input.FilePath, OldString: input.OldString, NewString: input.NewString,
					ReplaceAll: input.ReplaceAll, BaseHash: b.versions.base(input.FilePath),
				}, nil
			})
		}},
		offered: offeredFileTool(editFileToolName), build: buildFileTool,
	},
	{
		name: executeToolName, traits: sideEffectTraits(false), evidence: queryEvidence, writes: true, perAgent: true,
		// 命令语法随电脑的命令解释器说明，电脑提供托管运行环境时补充其用法。
		computer: &computerSpec{core: true, create: func(b *toolBuild) (*computerTool, error) {
			desc := fmt.Sprintf(executeToolDesc, b.request.ComputerCapabilities.Shell)
			if b.request.ComputerCapabilities.ManagedToolchain {
				desc += managedToolchainGuidance
			}
			return newComputerTool(executeToolName, desc, func(input executeArgs) (domain.ComputerOperation, error) {
				return domain.ComputerOperation{Kind: domain.ComputerOperationCommand, Command: input.Command}, nil
			})
		}},
		offered: offeredByComputer(executeToolName), build: buildComputerTool,
	},
	{
		name: ListSharedFilesToolName, traits: readOnlyTraits, evidence: queryEvidence,
		offered:  func(_ Scene, c Capabilities) bool { return c.SharedFiles },
		guidance: fixedGuidance(sharedFileToolGuidance),
		build: func(b *toolBuild, spec *toolSpec) (toolEntry, error) {
			created, err := newListSharedFilesTool(sharedFilesOf(b.request))
			return spec.entry(created), err
		},
	},
	{
		name: saveAttachmentToolName, traits: readOnlyTraits, evidence: queryEvidence, writes: true,
		offered: func(_ Scene, c Capabilities) bool { return c.SharedFiles },
		build: func(b *toolBuild, spec *toolSpec) (toolEntry, error) {
			created, err := newSaveAttachmentTool(sharedFilesOf(b.request))
			return spec.entry(created), err
		},
	},
	{
		name: browserToolName, traits: sideEffectTraits(false), evidence: queryEvidence, writes: true, perAgent: true,
		computer: &computerSpec{sequenced: true, provided: func(c domain.ComputerCapabilities) bool { return c.Browser }, create: func(*toolBuild) (*computerTool, error) {
			return newInterfaceTool(domain.ComputerOperationBrowser, browserToolName, browserToolDesc, domain.BrowserActions)
		}},
		offered: offeredByComputer(browserToolName), build: buildComputerTool,
		guidance: func(g guide) string { return fmt.Sprintf(browserToolGuidance, browserToolName, g.location) },
	},
	{
		name: desktopToolName, traits: sideEffectTraits(false), evidence: queryEvidence, writes: true, perAgent: true,
		computer: &computerSpec{sequenced: true, provided: func(c domain.ComputerCapabilities) bool { return c.Desktop }, create: func(*toolBuild) (*computerTool, error) {
			return newInterfaceTool(domain.ComputerOperationDesktop, desktopToolName, desktopToolDesc, domain.DesktopActions)
		}},
		offered: offeredByComputer(desktopToolName), build: buildComputerTool,
		guidance: func(g guide) string { return fmt.Sprintf(desktopToolGuidance, desktopToolName, g.location) },
	},
	{
		name: skillToolName, traits: readOnlyTraits, perAgent: true,
		computer: &computerSpec{facts: domain.ToolFacts{ReadOnly: true}, core: true, provided: func(c domain.ComputerCapabilities) bool { return len(c.Skills) > 0 }},
		offered:  offeredByComputer(skillToolName),
		guidance: fixedGuidance(fmt.Sprintf(skillToolGuidance, skillToolName)),
	},
	{
		name: localAgentToolName, traits: sideEffectTraits(false), evidence: queryEvidence, writes: true, perAgent: true, mainOnly: true,
		computer: &computerSpec{provided: func(c domain.ComputerCapabilities) bool { return len(c.LocalAgents) > 0 }, create: func(b *toolBuild) (*computerTool, error) {
			return newLocalAgentTool(b.request.ComputerCapabilities.LocalAgents)
		}},
		offered: offeredByComputer(localAgentToolName), build: buildComputerTool,
		guidance: func(g guide) string { return fmt.Sprintf(localAgentToolGuidance, localAgentToolName, g.location) },
	},
	{
		name: plantask.TaskCreateToolName, traits: readOnlyTraits, offered: internalScene,
		guidance: fixedGuidance(fmt.Sprintf(planToolGuidance, strings.Join(planToolNames, "、"))),
	},
	{name: plantask.TaskGetToolName, traits: readOnlyTraits, offered: internalScene},
	{name: plantask.TaskUpdateToolName, traits: readOnlyTraits, offered: internalScene},
	{name: plantask.TaskListToolName, traits: readOnlyTraits, offered: internalScene},
	{
		name: subagentToolName, traits: toolTraits{source: domain.AgentToolSourceDelegation}, offered: internalScene,
		guidance: fixedGuidance(subagentToolGuidance),
	},
	{
		name: CustomerHistoryToolName, traits: readOnlyTraits,
		offered: func(_ Scene, c Capabilities) bool { return c.CustomerHistory },
		build: func(b *toolBuild, spec *toolSpec) (toolEntry, error) {
			created, err := newCustomerHistoryTool(b.request.CustomerHistorySearch)
			return spec.entry(created), err
		},
		// 客服场景的回答受依据检查约束，客户历史的说明写明不能单独作为依据。
		guidance: func(g guide) string {
			if g.has(askCustomerToolName) {
				return customerSceneHistoryToolGuidance
			}
			return customerHistoryToolGuidance
		},
	},
	{
		name: askCustomerToolName, traits: readOnlyTraits, offered: serviceScene,
		guidance: fixedGuidance(askCustomerToolGuidance), build: buildTerminalTool,
	},
	{
		name: handoffToolName, traits: readOnlyTraits, offered: serviceScene, build: buildTerminalTool,
		guidance: func(g guide) string {
			if g.handoffCategories {
				return handoffCategoryToolGuidance
			}
			return handoffToolGuidance
		},
	},
	{
		name: resolveToolName, traits: readOnlyTraits, offered: serviceScene,
		guidance: fixedGuidance(resolveToolGuidance), build: buildTerminalTool,
	},
	{
		name: BusinessSystemToolCategory, category: true,
		offered: func(_ Scene, c Capabilities) bool { return len(c.BusinessSystems) > 0 },
		guidance: func(g guide) string {
			return fmt.Sprintf(businessSystemToolGuidance, strings.Join(g.businessSystems, "、"))
		},
	},
	{
		name: MCPToolCategory, category: true, perAgent: true, offered: offeredByComputer(MCPToolCategory),
		guidance: func(g guide) string { return fmt.Sprintf(mcpToolGuidance, g.location) },
	},
	{name: offloadedResultToolName, traits: readOnlyTraits, offered: func(Scene, Capabilities) bool { return true }, guidance: fixedGuidance(offloadedToolGuidance)},
}

// toolSpecIndex 是工具描述按名称的索引。
var toolSpecIndex = func() map[string]*toolSpec {
	index := make(map[string]*toolSpec, len(toolSpecs))
	for i := range toolSpecs {
		index[toolSpecs[i].name] = &toolSpecs[i]
	}
	return index
}()

// lookupToolSpec 按清单中的名称返回工具描述。
func lookupToolSpec(name string) (*toolSpec, bool) {
	spec, ok := toolSpecIndex[name]
	return spec, ok
}

// assignmentTools 按场景与执行侧能力从工具描述表推导有效配置的工具清单。
func assignmentTools(scene Scene, capabilities Capabilities) []string {
	return arr.OrEmpty(arr.FilterMap(toolSpecs, func(spec toolSpec) (string, bool) { return spec.name, spec.offered(scene, capabilities) }))
}

// guide 是生成工具说明所需的事实：清单列出的工具与类别、电脑位置与离线答复、咨询分类与业务系统名称。
type guide struct {
	listed            set.Set[string]
	computer          []string // 在运行使用的电脑上执行的文件与命令工具。
	location          string
	offline           string
	handoffCategories bool
	businessSystems   []string
	// customerLoginRequired 表示客户未验证身份，按客户查询的业务工具未挂载。
	customerLoginRequired bool
}

// has 判断清单是否列出工具或工具类别。
func (g guide) has(name string) bool { return g.listed.Has(name) }

// newGuide 按工具清单、执行侧能力与场景事实创建工具说明所需的事实；个人 AI 员工使用负责人的电脑，服务型 AI 员工使用工作区电脑。
func newGuide(tools []string, capabilities Capabilities, scene SceneContext) guide {
	g := guide{
		listed: set.Collect(tools), location: personalComputerLocation, offline: personalComputerOffline,
		handoffCategories: len(scene.HandoffCategories) > 0, businessSystems: businessSystemNames(capabilities),
		customerLoginRequired: capabilities.CustomerLoginRequired,
	}
	if capabilities.WorkspaceComputer != "" {
		g.location, g.offline = fmt.Sprintf(workspaceComputerLocation, capabilities.WorkspaceComputer), workspaceComputerOffline
	}
	for _, name := range []string{readFileToolName, writeFileToolName, editFileToolName, executeToolName} {
		if slices.Contains(capabilities.ComputerTools, name) {
			g.computer = append(g.computer, name)
		}
	}
	return g
}

// toolGuidance 按工具描述表的顺序生成清单列出的工具的说明，没有需要说明的工具时返回空串；提供联网工具时补充来源要求。
func toolGuidance(g guide) string {
	lines := make([]string, 0, len(g.listed))
	for _, spec := range toolSpecs {
		if spec.guidance == nil || !g.has(spec.name) {
			continue
		}
		if line := spec.guidance(g); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	guidance := "可用工具：\n" + strings.Join(lines, "\n")
	if g.has(WebSearchToolName) || g.has(WebFetchToolName) {
		guidance = joinSections(guidance, webSourceGuidance)
	}
	return guidance
}
