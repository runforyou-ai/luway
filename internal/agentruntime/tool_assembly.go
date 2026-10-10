package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/cloudwego/eino/components/tool"
	"github.com/eino-contrib/jsonschema"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/set"
)

// toolBuild 是构造工具时可取用的运行依赖与本次运行共用的执行期状态；agent 是按 Agent 构造的工具所属 Agent 的标识。
type toolBuild struct {
	request   RunRequest
	terminal  *terminalTools
	versions  *fileVersions
	sequencer *interfaceSequencer // 有效配置不含浏览器与桌面工具时为空。
	images    bool                // 模型能识别图片，共享文件区读到的图片附给模型。
	agent     string
}

// toolTraits 是工具的来源、中断后能否重新执行、是否产生外部副作用与执行前的操作级别和人工介入。
type toolTraits struct {
	source      domain.AgentToolSource
	replayable  bool
	sideEffects bool
	policy      agentcontract.ToolPolicy
	// shared 判断文件工具的调用是否读写会话共享文件区，读写文件区的调用按 sharedFileTraits 登记；其余工具为空。
	shared func(arguments string) bool
}

// sharedFileTraits 是读写会话共享文件区的文件工具调用的特性：由服务端在事务内完成，可重新执行，不需要人工介入。
var sharedFileTraits = toolTraits{source: domain.AgentToolSourceBuiltin, replayable: true}

// externalToolRef 是业务系统工具与本机 MCP 工具在调用记录中的原工具名、所属服务与绑定参数。
type externalToolRef struct {
	name             string
	mcpServer        string
	businessSystemID string
	businessSystem   string
	bound            map[string]string
}

// toolEntry 是一次运行注册的一个工具：模型可见名称、所属描述、Eino 工具（由运行时或内置扩展注册时为空）、特性、依据判定、外部来源与派发目标；
// 按 Agent 构造的工具以 create 为每个 Agent 创建实例。
type toolEntry struct {
	name     string
	spec     *toolSpec // 动态工具为其类别的描述。
	tool     tool.BaseTool
	traits   toolTraits
	evidence evidenceJudge
	writes   bool
	external *externalToolRef
	target   func(arguments string) (agentcontract.ComputerTarget, error)
	create   func(agent string) (tool.BaseTool, error)
	// inline 表示需要确认的调用暂停运行，确认后在本次运行内执行。
	inline bool
}

// entry 返回按工具描述登记特性与依据的注册项。
func (s *toolSpec) entry(item tool.BaseTool) toolEntry {
	return toolEntry{name: s.name, spec: s, tool: item, traits: s.traits, evidence: s.evidence, writes: s.writes}
}

// registered 判断工具由本运行注册，运行时或内置扩展注册的工具返回 false。
func (e toolEntry) registered() bool { return e.tool != nil || e.create != nil }

// notes 返回写入工具每次调用记录的业务注解。
func (e toolEntry) notes() map[string]string {
	notes := map[string]string{agentcontract.NoteSource: string(e.traits.source)}
	if e.traits.policy.Level != "" {
		notes[agentcontract.NoteLevel] = string(e.traits.policy.Level)
	}
	if ref := e.external; ref != nil {
		if ref.mcpServer != "" {
			notes[agentcontract.NoteMCPServer] = ref.mcpServer
		}
		if ref.businessSystemID != "" {
			notes[agentcontract.NoteBusinessSystemID], notes[agentcontract.NoteBusinessSystemName] = ref.businessSystemID, ref.businessSystem
		}
		if len(ref.bound) > 0 {
			bound, _ := json.Marshal(ref.bound)
			notes[agentcontract.NoteBoundArguments] = string(bound)
		}
	}
	return notes
}

// toolSpec 把注册项转换为运行时的工具规格：按 Agent 构造的工具为每个 Agent 创建实例，外部工具记录原工具名，读写共享文件区的调用按共享文件特性登记，
// 需要人工介入的调用提交确认或审批；retain 为结果在上下文治理中的保留方式。
func (e toolEntry) toolSpec(retain einorun.Retain) einorun.ToolSpec {
	spec := einorun.ToolSpec{
		Replayable: e.traits.replayable, SideEffects: e.traits.sideEffects, MainOnly: e.spec.mainOnly,
		Notes: e.notes(), Retain: retain, Completion: isTerminalToolName(e.name),
	}
	if create := e.create; create != nil {
		spec.New = func(_ context.Context, scope einorun.AgentScope) (tool.BaseTool, func(), error) {
			created, err := create(scope.ID)
			return created, nil, err
		}
	} else {
		spec.Tool = e.tool
	}
	if ref := e.external; ref != nil {
		name := ref.name
		spec.RecordName = func(string) string { return name }
	}
	if e.traits.shared != nil || e.traits.policy.Intervention != domain.ToolInterventionNone {
		spec.Policy = e.policy
	}
	return spec
}

// policy 按完整参数决定一次调用：读写共享文件区的调用按共享文件特性登记且直接执行，发起人在场的确认暂停运行，其余需要人工介入的调用提交确认或审批；
// 电脑工具调用的参数无法转换为批准后派发的操作时调用失败，原因交给模型。
func (e toolEntry) policy(_ context.Context, call einorun.CallView) (einorun.CallPolicy, error) {
	if e.traits.shared != nil && e.traits.shared(call.Arguments) {
		return einorun.CallPolicy{
			Replayable: new(sharedFileTraits.replayable), SideEffects: new(sharedFileTraits.sideEffects),
			Notes: map[string]string{agentcontract.NoteLevel: ""},
		}, nil
	}
	// 发起人在场的确认在主 Agent 中暂停运行，其余确认与审批提交给处理人。
	if e.inline && call.Agent.Main && e.traits.policy.Intervention == domain.ToolInterventionConfirmation {
		return confirmation(e.traits.policy.Intervention, e.target, call.Arguments)
	}
	return submission(e.traits.policy.Intervention, e.target, call.Arguments)
}

// confirmation 返回暂停运行等待确认的决定，载荷与提交相同，记下需要的人工介入与电脑工具调用的派发目标。
func confirmation(intervention domain.ToolIntervention, target func(string) (agentcontract.ComputerTarget, error), arguments string) (einorun.CallPolicy, error) {
	submitted, err := submission(intervention, target, arguments)
	if err != nil {
		return einorun.CallPolicy{}, err
	}
	return einorun.CallPolicy{Confirm: &einorun.Confirmation{Payload: submitted.Submit.Payload}}, nil
}

// confirmsInline 判断场景中需要确认的调用是否由在场的发起人当场确认：个人 AI 员工对话与 Copilot 中发起人就是对话的成员。
func confirmsInline(scene Scene) bool {
	return scene == SceneAgentChat || scene == SceneCopilot
}

// submission 返回需要人工介入的调用的提交决定，target 为空表示调用不派发到电脑；不需要人工介入时返回空决定。
func submission(intervention domain.ToolIntervention, target func(string) (agentcontract.ComputerTarget, error), arguments string) (einorun.CallPolicy, error) {
	if intervention == domain.ToolInterventionNone {
		return einorun.CallPolicy{}, nil
	}
	submitted := agentcontract.Submission{Intervention: intervention}
	if target != nil {
		resolved, err := target(arguments)
		if err != nil {
			return einorun.CallPolicy{}, err
		}
		submitted.Target = &resolved
	}
	payload, err := json.Marshal(submitted)
	if err != nil {
		return einorun.CallPolicy{}, err
	}
	return einorun.CallPolicy{Submit: &einorun.Submission{Receipt: agentcontract.SubmittedResult(intervention), Payload: payload}}, nil
}

// unavailableComputer 是清单列出电脑工具而本次运行没有可用电脑或授权不允许时使用的电脑，操作一律不执行。
type unavailableComputer struct{}

// Execute 不执行操作并返回不可用。
func (unavailableComputer) Execute(context.Context, domain.ComputerOperation, bool) (domain.ComputerOutcome, error) {
	return domain.ComputerOutcome{}, errToolUnavailable
}

// Target 返回没有执行电脑的操作。
func (unavailableComputer) Target(operation domain.ComputerOperation) agentcontract.ComputerTarget {
	return agentcontract.ComputerTarget{Operation: operation}
}

// Submit 不交给本机 Agent 并返回不可用。
func (unavailableComputer) Submit(context.Context, domain.ComputerOperation) (string, error) {
	return "", errToolUnavailable
}

// unavailableSharedFiles 是清单列出共享文件工具而本次运行没有共享文件区时使用的文件区，操作一律不执行。
type unavailableSharedFiles struct{}

// List 返回不可用。
func (unavailableSharedFiles) List(context.Context) ([]SharedFile, error) {
	return nil, errToolUnavailable
}

// Read 返回不可用。
func (unavailableSharedFiles) Read(context.Context, string) (SharedFileContent, error) {
	return SharedFileContent{}, errToolUnavailable
}

// Write 返回不可用。
func (unavailableSharedFiles) Write(context.Context, string, []byte, string) (SharedFile, error) {
	return SharedFile{}, errToolUnavailable
}

// SaveAttachment 返回不可用。
func (unavailableSharedFiles) SaveAttachment(context.Context, string, string) (SharedFile, error) {
	return SharedFile{}, errToolUnavailable
}

// buildTerminalTool 取出服务场景的终止工具，非服务场景的清单列出终止工具时返回错误。
func buildTerminalTool(b *toolBuild, spec *toolSpec) (toolEntry, error) {
	if b.terminal == nil {
		return toolEntry{}, fmt.Errorf("agent run assignment lists terminal tool %s outside a service scene", spec.name)
	}
	return spec.entry(b.terminal.tool(spec.name)), nil
}

// sharedFilesOf 返回运行的共享文件区，没有时返回不执行操作的文件区。
func sharedFilesOf(request RunRequest) SharedFiles {
	if request.SharedFiles == nil {
		return unavailableSharedFiles{}
	}
	return request.SharedFiles
}

// newSpecComputerTool 按工具描述创建派发到电脑的工具，按授权取得操作级别与人工介入并在说明末尾注明；没有电脑或授权不允许时调用返回不可用，第二个返回值为 false。
func newSpecComputerTool(b *toolBuild, spec *toolSpec) (*computerTool, bool, error) {
	created, err := spec.computer.create(b)
	if err != nil {
		return nil, false, fmt.Errorf("create computer tool %s: %w", spec.name, err)
	}
	policy, permitted := b.request.ComputerAccess.policy(spec.computer.facts)
	available := permitted && b.request.Computer != nil
	created.computer, created.versions, created.agent = b.request.Computer, b.versions, b.agent
	if !available {
		created.computer, policy = unavailableComputer{}, agentcontract.ToolPolicy{}
	}
	if spec.computer.sequenced {
		created.sequencer = b.sequencer
	}
	created.policy = policy
	if note, ok := interventionNotes[policy.Intervention]; ok {
		created.info.Desc += note
	}
	return created, available, nil
}

// computerEntry 返回电脑工具的注册项，特性带上操作级别与人工介入。
func computerEntry(spec *toolSpec, created *computerTool) toolEntry {
	entry := spec.entry(created)
	entry.traits.policy = created.policy
	entry.target = created.target
	return entry
}

// buildComputerTool 创建派发到电脑的工具。
func buildComputerTool(b *toolBuild, spec *toolSpec) (toolEntry, error) {
	created, _, err := newSpecComputerTool(b, spec)
	if err != nil {
		return toolEntry{}, err
	}
	return computerEntry(spec, created), nil
}

// buildFileTool 创建文件工具：有共享文件区时以 shared/ 开头的路径读写共享文件区，其余路径交给可用的同名电脑工具，读写共享文件区的调用按共享文件特性登记；没有共享文件区时就是电脑工具。
func buildFileTool(b *toolBuild, spec *toolSpec) (toolEntry, error) {
	created, available, err := newSpecComputerTool(b, spec)
	if err != nil {
		return toolEntry{}, err
	}
	if b.request.SharedFiles == nil {
		return computerEntry(spec, created), nil
	}
	if !available {
		created = nil
	}
	shared, err := newSharedFileTool(spec.name, b.request.SharedFiles, b.versions, b.images, created)
	if err != nil {
		return toolEntry{}, err
	}
	entry := spec.entry(shared)
	if created != nil {
		entry.traits.policy, entry.target = created.policy, created.target
	} else {
		entry.traits = sharedFileTraits
	}
	entry.traits.shared = sharedArguments
	return entry, nil
}

// mcpParameters 解析本机 MCP 工具的参数定义，没有定义时为空对象。
func mcpParameters(item domain.ComputerMCPTool) (*jsonschema.Schema, error) {
	parameters := &jsonschema.Schema{Type: "object"}
	if len(item.InputSchema) > 0 {
		if err := json.Unmarshal(item.InputSchema, parameters); err != nil {
			return nil, err
		}
	}
	return parameters, nil
}

// mcpEntry 按授权创建电脑上一个本机 MCP 工具的注册项，授权不允许时返回 false；没有电脑时调用返回不可用。
func mcpEntry(b *toolBuild, spec *toolSpec, server string, item domain.ComputerMCPTool, parameters *jsonschema.Schema) (toolEntry, bool) {
	policy, ok := b.request.ComputerAccess.policy(domain.HintedToolFacts(item.ReadOnlyHint, item.DestructiveHint))
	if !ok {
		return toolEntry{}, false
	}
	created := newComputerMCPTool(server, item, parameters)
	created.policy, created.computer, created.versions, created.agent = policy, b.request.Computer, b.versions, b.agent
	if created.computer == nil {
		created.computer, created.policy = unavailableComputer{}, agentcontract.ToolPolicy{}
	}
	if note, ok := interventionNotes[created.policy.Intervention]; ok {
		created.info.Desc += note
	}
	return toolEntry{
		name: created.info.Name, spec: spec, tool: created,
		traits:   toolTraits{source: domain.AgentToolSourceMCP, sideEffects: true, policy: created.policy},
		evidence: queryEvidence, writes: created.policy.Level != domain.OperationLevelL0,
		external: &externalToolRef{name: item.Name, mcpServer: server}, target: created.target,
	}, true
}

// buildMCPTools 按电脑上报的本机 MCP 服务与授权创建本机 MCP 工具，授权不允许或参数定义无法解析的工具不注册；每个 Agent 各自创建实例。
func buildMCPTools(ctx context.Context, b *toolBuild, spec *toolSpec) []toolEntry {
	entries := make([]toolEntry, 0)
	for _, server := range b.request.ComputerCapabilities.MCPServers {
		for _, item := range server.Tools {
			parameters, err := mcpParameters(item)
			if err != nil {
				slog.WarnContext(ctx, "本机 MCP 工具参数定义无法解析，跳过该工具",
					"agent_run_id", b.request.RunID, "mcp_server", server.Name, "tool_name", item.Name, "error", err)
				continue
			}
			entry, ok := mcpEntry(b, spec, server.Name, item, parameters)
			if !ok {
				continue
			}
			// 每个 Agent 按自己的标识创建实例。
			entry.create = func(agent string) (tool.BaseTool, error) {
				scoped := *b
				scoped.agent = agent
				created, _ := mcpEntry(&scoped, spec, server.Name, item, parameters)
				return created.tool, nil
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

// buildBusinessTools 按业务系统顺序注册挂载的业务系统工具，跳过名称与已注册工具重复的工具。
func buildBusinessTools(ctx context.Context, request RunRequest, spec *toolSpec, registered set.Set[string]) []toolEntry {
	entries := make([]toolEntry, 0)
	for _, item := range newBusinessTools(ctx, request.RunID, request.BusinessSystems, registered) {
		entries = append(entries, toolEntry{
			name: item.info.Name, spec: spec, tool: item,
			traits:   toolTraits{source: domain.AgentToolSourceBusinessSystem, replayable: item.mount.ReadOnly, sideEffects: !item.mount.ReadOnly, policy: item.mount.ToolPolicy},
			evidence: queryEvidence, writes: !item.mount.ReadOnly,
			external: &externalToolRef{name: item.mount.Name, businessSystemID: item.system.ID, businessSystem: item.system.Name, bound: item.mount.Bound},
		})
	}
	return entries
}

// assembleTools 按有效配置的工具清单从工具描述表装配工具注册项：按 Agent 构造的工具为每个 Agent 各建实例，运行时与内置扩展注册的工具只登记名称，
// 业务系统工具在其余工具之后按业务系统顺序注册并跳过重名工具；装配结果与清单双向核对，不一致时返回错误。注册项按清单顺序排列。
func assembleTools(ctx context.Context, b *toolBuild) ([]toolEntry, error) {
	request := b.request
	var entries []toolEntry
	var business *toolSpec
	for _, name := range request.Assignment.Tools {
		spec, ok := lookupToolSpec(name)
		if !ok {
			continue
		}
		switch {
		case spec.category && spec.perAgent:
			entries = append(entries, buildMCPTools(ctx, b, spec)...)
		case spec.category:
			business = spec
		case spec.build != nil:
			entry, err := spec.build(b, spec)
			if err != nil {
				return nil, err
			}
			// 每个 Agent 按自己的标识重新构造按 Agent 区分的工具。
			if spec.perAgent {
				entry.create = func(agent string) (tool.BaseTool, error) {
					scoped := *b
					scoped.agent = agent
					created, err := spec.build(&scoped, spec)
					return created.tool, err
				}
			}
			entries = append(entries, entry)
		default:
			entries = append(entries, spec.entry(nil))
		}
	}
	// 收齐其余工具名称，业务系统工具重名时跳过。
	registered := set.CollectBy(entries, func(entry toolEntry) string { return entry.name })
	if business != nil {
		entries = append(entries, buildBusinessTools(ctx, request, business, registered)...)
	}
	if err := verifyManifest(request.Assignment.Tools, entries); err != nil {
		return nil, err
	}
	order := make(map[string]int, len(request.Assignment.Tools))
	for index, name := range request.Assignment.Tools {
		order[name] = index
	}
	slices.SortStableFunc(entries, func(x, y toolEntry) int { return order[x.spec.name] - order[y.spec.name] })
	return entries, nil
}

// verifyManifest 核对装配结果与有效配置的工具清单：清单中每个工具都已注册，每个注册的工具都由清单按名称或类别列出。
func verifyManifest(manifest []string, entries []toolEntry) error {
	listed := make(map[string]bool, len(manifest))
	for _, name := range manifest {
		if _, ok := lookupToolSpec(name); !ok {
			return fmt.Errorf("agent run assignment lists unknown tool %q", name)
		}
		listed[name] = true
	}
	var registered set.Set[string]
	for _, entry := range entries {
		if !registered.Add(entry.name) {
			return fmt.Errorf("agent run registered tool %q more than once", entry.name)
		}
		if !listed[entry.spec.name] {
			return fmt.Errorf("agent run registered tool %q outside its assignment %v", entry.name, manifest)
		}
	}
	for _, name := range manifest {
		if spec, _ := lookupToolSpec(name); !spec.category && !registered.Has(name) {
			return fmt.Errorf("agent run assignment lists tool %q that is not registered", name)
		}
	}
	return nil
}
