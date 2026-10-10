package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	// subagentToolName 是把子任务委派给子 Agent 的工具名称。
	subagentToolName = "agent"
	// subagentName 是通用子 Agent 的类型名称。
	subagentName = "general"
	// offloadedResultToolName 是读回转存工具结果的工具名称，每次运行都注册。
	offloadedResultToolName = einorun.OffloadReadTool
	// subagentDescription 是通用子 Agent 的适用范围说明。
	subagentDescription = "通用子 Agent：使用与你相同的工具（不含 agent 与任务清单工具），适合调研资料、查阅大量文件、独立完成一段修改等可以单独交付结果的任务。"
)

// planToolNames 是任务清单工具，按注册顺序排列。
var planToolNames = einorun.PlanTools

// modelModalities 把模型声明的输入模态对应到运行时的输入模态，文本不需要声明。
var modelModalities = map[domain.AIModelInputModality]llm.Modality{
	domain.AIModelInputModalityImage: llm.Image,
	domain.AIModelInputModalityAudio: llm.Audio,
	domain.AIModelInputModalityVideo: llm.Video,
}

// EinoRuntime 把平台托管 Agent 的运行交给 einorun 执行。
type EinoRuntime struct {
	runtime *einorun.Runtime
}

// New 创建 Agent 运行时：框架内置提示与本项目面向模型的提示统一使用中文，客服场景同批违规的提示补充终止工具的用法。
func New() (*EinoRuntime, error) {
	if err := adk.SetLanguage(adk.LanguageChinese); err != nil {
		return nil, fmt.Errorf("set agent runtime language: %w", err)
	}
	return &EinoRuntime{runtime: einorun.New(einorun.Config{Language: llm.Chinese, Text: einorun.Text{
		BatchTooMany: "%s 一次只能调用其中一个" + terminalGuidance + "。",
		BatchMixed:   "%s 必须单独调用，不能与其他工具同时调用" + terminalGuidance + "。",
	}})}, nil
}

// Run 按有效配置装配并执行一次运行：服务场景以终止工具或转人工结束，其余场景以正文作为回答；挂起或出错时一并给出已产生的用量、内容块与任务清单。
func (r *EinoRuntime) Run(ctx context.Context, request RunRequest, feed einorun.Feed) (RunResult, error) {
	if feed == nil {
		return RunResult{}, errors.New("agent input feed is required")
	}
	defer closeBusinessCallers(request.BusinessSystems)
	assembled, err := r.assemble(ctx, request, feed)
	if err != nil {
		return RunResult{}, err
	}
	result, err := r.runtime.Run(ctx, assembled)
	out := RunResult{
		Usage: agentcontract.UsageFrom(result.Usage), Blocks: result.Blocks, Calls: result.Calls, Plan: result.Plan, Suspended: result.Suspended,
	}
	if err != nil || result.Suspended {
		return out, err
	}
	out.EndSeq, out.Content = result.EndSeq, result.Text
	if result.Completion != nil {
		out.Decision, out.Content, err = decodeCompletion(result.Completion)
	}
	return out, err
}

// assemble 按有效配置组装一次 einorun 运行：工具规格、文件内容摘要、界面调用排序、依据门禁、任务清单、委派、技能与记忆扩展，服务场景的完成策略，以及附件与共享文件的读取。
func (r *EinoRuntime) assemble(ctx context.Context, request RunRequest, feed einorun.Feed) (einorun.Request, error) {
	assignment := request.Assignment
	service := assignment.Scene.Service()
	config := request.modelConfig()
	modalities := make([]llm.Modality, 0, len(config.InputModalities))
	for _, modality := range config.InputModalities {
		if mapped, ok := modelModalities[modality]; ok {
			modalities = append(modalities, mapped)
		}
	}
	versions := newFileVersions()
	b := &toolBuild{request: request, versions: versions, images: slices.Contains(modalities, llm.Image), agent: einorun.MainAgentID}
	for _, name := range assignment.Tools {
		if spec, ok := lookupToolSpec(name); ok && spec.computer != nil && spec.computer.sequenced {
			b.sequencer = newInterfaceSequencer()
			break
		}
	}
	var gate *groundingGate
	var completion *einorun.CompletionPolicy
	if service {
		b.terminal, completion = newTerminalTools(assignment.HandoffCategories), terminalPolicy()
		if assignment.Grounding == GroundingStrict {
			gate = newGroundingGate()
		}
	}
	entries, err := assembleTools(ctx, b)
	if err != nil {
		return einorun.Request{}, err
	}
	extensions := []einorun.Extension{versions}
	if b.sequencer != nil {
		extensions = append(extensions, b.sequencer)
	}
	tools := make([]einorun.ToolSpec, 0, len(entries))
	for _, entry := range entries {
		// 严格依据策略下依据来源工具的结果不参与清理，上下文增长交由摘要压缩，摘要原样保留有效依据。
		retain := einorun.RetainDefault
		if gate != nil && entry.evidence != nil {
			gate.judges[entry.name] = entry.evidence
			if entry.writes {
				gate.writes[entry.name] = struct{}{}
			}
			retain = einorun.RetainKeep
		}
		if entry.registered() {
			entry.inline = confirmsInline(assignment.Scene)
			tools = append(tools, entry.toolSpec(retain))
		}
	}
	builtins := map[string]einorun.BuiltinTool{}
	if slices.Contains(assignment.Tools, plantaskToolName()) {
		extensions = append(extensions, einorun.Planning())
	}
	if slices.Contains(assignment.Tools, subagentToolName) {
		extensions = append(extensions, einorun.Subagent(einorun.SubagentSpec{
			ToolName: subagentToolName, Name: subagentName, Description: subagentDescription,
			Instruction: assignment.DelegateInstruction, MaxIterations: request.MaxIterations,
		}))
		builtins[subagentToolName] = einorun.BuiltinTool{Notes: map[string]string{agentcontract.NoteSource: string(domain.AgentToolSourceDelegation)}}
	}
	if skills := newSkillsExtension(request); skills != nil {
		extensions = append(extensions, skills)
		builtins[skillToolName] = skillBuiltin(request)
	}
	if recall := newMemoryRecall(ctx, request); recall != nil {
		extensions = append(extensions, recall)
	}
	var guard einorun.Guard
	if gate != nil {
		extensions = append(extensions, gate)
		guard = gate
	}
	return einorun.Request{
		RunID: request.RunID, Instruction: assignment.Instruction,
		Model: einorun.Model{New: config.New, ContextWindow: config.ContextWindow, MaxOutputTokens: config.MaxOutputTokens, Inputs: modalities},
		Tools: tools, Feed: feed, Journal: request.Journal, Resume: request.Resume, ReadMedia: readMedia(request),
		Stream: request.OnStream, StreamID: request.StreamID,
		Limits:     einorun.Limits{MaxIterations: request.MaxIterations, MaxTurns: request.MaxTurns, Corrections: correctionLimit},
		Completion: completion, Guard: guard, Extensions: extensions, BuiltinTools: builtins,
		// 服务场景不在系统指令中追加上下文管理说明，未发出的正文不进入后续轮次的历史。
		Context: einorun.ContextPolicy{OmitManagementNote: service}, DiscardUndelivered: service,
	}, nil
}

// plantaskToolName 返回代表任务清单的工具名称，清单列出它时注册全部任务清单工具。
func plantaskToolName() string { return planToolNames[0] }

// skillBuiltin 返回技能工具的调用注解与人工介入：加载技能按电脑授权取得操作级别，需要人工介入时提交确认或审批。
func skillBuiltin(request RunRequest) einorun.BuiltinTool {
	notes := map[string]string{agentcontract.NoteSource: string(domain.AgentToolSourceBuiltin)}
	spec, _ := lookupToolSpec(skillToolName)
	policy, permitted := request.ComputerAccess.policy(spec.computer.facts)
	if !permitted || request.Computer == nil {
		return einorun.BuiltinTool{Notes: notes}
	}
	if policy.Level != "" {
		notes[agentcontract.NoteLevel] = string(policy.Level)
	}
	builtin := einorun.BuiltinTool{Notes: notes}
	if policy.Intervention != domain.ToolInterventionNone {
		builtin.Policy = func(_ context.Context, call einorun.CallView) (einorun.CallPolicy, error) {
			return submission(policy.Intervention, nil, call.Arguments)
		}
	}
	return builtin
}

// readMedia 按媒体键读取运行中附给模型的媒体：会话附件经执行侧按附件消息编号读取，共享文件区的图片按文件区路径读取。
func readMedia(request RunRequest) einorun.MediaReader {
	return func(ctx context.Context, ref einorun.MediaRef) ([]byte, error) {
		if messageID, ok := strings.CutPrefix(ref.Key, agentcontract.MediaKeyMessage); ok {
			if request.ReadAttachment == nil {
				return nil, errors.New("agent run has no attachment reader")
			}
			return request.ReadAttachment(ctx, messageID)
		}
		if path, ok := strings.CutPrefix(ref.Key, agentcontract.MediaKeyShared); ok {
			if request.SharedFiles == nil {
				return nil, errors.New("agent run has no shared files")
			}
			content, err := request.SharedFiles.Read(ctx, path)
			if err != nil {
				return nil, err
			}
			if content.Data == nil {
				return nil, fmt.Errorf("shared file %s has no readable content", path)
			}
			return content.Data, nil
		}
		return nil, fmt.Errorf("unknown agent run media key %q", ref.Key)
	}
}
