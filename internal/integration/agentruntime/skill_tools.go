package agentruntime

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	// skillToolName 是加载技能说明的工具名称。
	skillToolName = SkillToolName
	// maxSkillResources 是加载技能时列出的附带文件数量上限。
	maxSkillResources = 100
)

const skillToolDesc = `加载一个技能的完整说明，按说明完成任务。
- skill 填写可用技能的名称，args 是可选的附加说明。
- 同一个技能在本次运行中加载过时不重复加载，直接按上文说明执行。

可用技能：
%s`

// forkSkillNote 标在交给子 Agent 执行的技能简介之后。
const forkSkillNote = "（独立执行：由子 Agent 完成，它看不到本次对话，调用时在 args 写清用户的目标、已知信息和相关文件路径）"

// skillBackend 把电脑上报的技能目录提供给技能中间件，经电脑读取技能说明，并按运行记录已加载的技能说明。
type skillBackend struct {
	computer Computer
	skills   []domain.ComputerSkill
	managed  bool // 电脑提供托管运行环境，技能说明后补充依赖安装方式。
	fork     bool // 声明 context: fork 的技能交给子 Agent 执行，为 false 时在当前上下文加载。
	mu       sync.Mutex
	loaded   map[string][32]byte // 技能名称到本次运行已加载说明的摘要。
	files    map[string][]string // 技能名称到最近一次读取到的附带文件。
}

// List 返回可用技能的名称与简介，并标出交给子 Agent 执行的技能。
func (b *skillBackend) List(context.Context) ([]skill.FrontMatter, error) {
	matters := make([]skill.FrontMatter, len(b.skills))
	for i, item := range b.skills {
		matters[i] = skill.FrontMatter{Name: item.Name, Description: item.Description}
		if item.Fork && b.fork {
			matters[i].Context = skill.ContextModeFork
		}
	}
	return matters, nil
}

// Get 经电脑读取技能的说明正文与附带文件，返回名称、简介、正文与所在目录；读取结果需要加工后才交给模型，调用不挂起运行。
func (b *skillBackend) Get(ctx context.Context, name string) (skill.Skill, error) {
	index := slices.IndexFunc(b.skills, func(item domain.ComputerSkill) bool { return item.Name == name })
	if index < 0 {
		// 名称不存在时列出可用技能供模型改正。
		names := make([]string, len(b.skills))
		for i, item := range b.skills {
			names[i] = item.Name
		}
		available := "无"
		if len(names) > 0 {
			available = strings.Join(names, "、")
		}
		return skill.Skill{}, fmt.Errorf("没有名为 %s 的技能，可用技能：%s", name, available)
	}
	item := b.skills[index]
	outcome, err := b.computer.Execute(withoutSuspend(ctx), domain.ComputerOperation{Kind: domain.ComputerOperationLoadSkill, Skill: name}, false)
	if err != nil {
		return skill.Skill{}, err
	}
	b.mu.Lock()
	b.files[name] = outcome.Files
	b.mu.Unlock()
	// 技能声明的模型不生效，声明 fork 的技能在可委派时交给子 Agent。
	matter := skill.FrontMatter{Name: item.Name, Description: item.Description}
	if item.Fork && b.fork {
		matter.Context = skill.ContextModeFork
	}
	return skill.Skill{FrontMatter: matter, Content: outcome.Output, BaseDirectory: cmp.Or(outcome.Path, item.Dir)}, nil
}

// content 返回技能加载结果：正文、技能目录与附带文件清单以 skill_content 包裹；本次运行已在当前上下文加载过同一份说明时返回提示，交给子 Agent 执行的技能每次给出完整说明，缺少任务说明时返回错误。
func (b *skillBackend) content(_ context.Context, loaded skill.Skill, rawArguments string) (string, error) {
	var arguments struct {
		Args string `json:"args"`
	}
	_ = json.Unmarshal([]byte(rawArguments), &arguments)
	if loaded.Context == skill.ContextModeFork && strings.TrimSpace(arguments.Args) == "" {
		return "", fmt.Errorf("技能 %s 由子 Agent 在独立上下文中执行，看不到本次对话；请在 args 中写清用户的目标、已知信息和相关文件路径后重新调用", loaded.Name)
	}
	digest := sha256.Sum256([]byte(loaded.BaseDirectory + "\x00" + loaded.Content))
	repeated := false
	b.mu.Lock()
	resources := b.files[loaded.Name]
	if loaded.Context != skill.ContextModeFork {
		repeated = b.loaded[loaded.Name] == digest
		b.loaded[loaded.Name] = digest
	}
	b.mu.Unlock()
	if repeated {
		return fmt.Sprintf("技能 %s 的说明已在上文加载，直接按其执行。", loaded.Name), nil
	}
	var text strings.Builder
	fmt.Fprintf(&text, "<skill_content name=%q>\n%s\n\n技能目录：%s\n技能中的相对路径以技能目录为起点，读取文件与运行脚本时使用绝对路径。\n", loaded.Name, loaded.Content, loaded.BaseDirectory)
	// 列出技能附带的文件，内容由模型按需读取。
	if len(resources) > 0 {
		text.WriteString("<skill_resources>\n")
		for _, resource := range resources[:min(len(resources), maxSkillResources)] {
			fmt.Fprintf(&text, "<file>%s</file>\n", resource)
		}
		if len(resources) > maxSkillResources {
			fmt.Fprintf(&text, "<!-- 只列出前 %d 个文件 -->\n", maxSkillResources)
		}
		text.WriteString("</skill_resources>\n")
	}
	if b.managed {
		text.WriteString("技能说明中安装 Python 依赖或运行脚本的方式按 execute 工具说明中的托管运行环境用法执行，例如用 uv run --with 包名 代替 pip install。\n")
	}
	text.WriteString("</skill_content>")
	if strings.TrimSpace(arguments.Args) != "" {
		text.WriteString("\n\n附加说明：" + arguments.Args)
	}
	return text.String(), nil
}

// newSkillTools 按有效配置创建技能中间件，有效配置不含技能工具时返回空值；技能目录取自电脑在运行开始时上报的能力，hub 非空时声明 fork 的技能交给其提供的子 Agent 执行。
func newSkillTools(ctx context.Context, request RunRequest, hub skill.TypedAgentHub[*schema.AgenticMessage]) (adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], error) {
	if !slices.Contains(request.Assignment.Tools, skillToolName) {
		return nil, nil
	}
	if request.Computer == nil {
		return nil, fmt.Errorf("agent run assignment requires skill tools without a computer")
	}
	backend := &skillBackend{
		computer: request.Computer, skills: request.ComputerCapabilities.Skills, managed: request.ComputerCapabilities.ManagedToolchain,
		fork: hub != nil, loaded: make(map[string][32]byte), files: make(map[string][]string),
	}
	middleware, err := skill.NewTyped(ctx, &skill.TypedConfig[*schema.AgenticMessage]{
		Backend:    backend,
		UseChinese: true,
		// 技能用法由场景规则中的工具说明提供。
		CustomSystemPrompt: func(context.Context, string) string { return "" },
		// 可用技能随工具说明提供，取自电脑在运行开始时上报的技能目录。
		CustomToolDescription: func(_ context.Context, skills []skill.FrontMatter) string {
			if len(skills) == 0 {
				return fmt.Sprintf(skillToolDesc, "（暂无）")
			}
			lines := make([]string, len(skills))
			for i, item := range skills {
				lines[i] = "- " + item.Name + "：" + item.Description
				if item.Context == skill.ContextModeFork {
					lines[i] += forkSkillNote
				}
			}
			return fmt.Sprintf(skillToolDesc, strings.Join(lines, "\n"))
		},
		CustomFormatReminder: func(context.Context, *skill.FormatReminderInput) (*skill.FormatReminderOutput, error) {
			return &skill.FormatReminderOutput{}, nil
		},
		// 技能名称不限定枚举，名称不存在时由工具返回可用技能。
		CustomToolParams: func(_ context.Context, defaults map[string]*schema.ParameterInfo) (map[string]*schema.ParameterInfo, error) {
			defaults["skill"].Desc = "技能名称"
			defaults["args"].Desc = "传给技能的附加说明；独立执行的技能以它作为完整的任务说明"
			return defaults, nil
		},
		BuildContent: backend.content,
		AgentHub:     hub,
		// 子 Agent 的最终回复作为技能的执行结果。
		FormatForkResult: func(_ context.Context, output skill.TypedSubAgentOutput[*schema.AgenticMessage]) (string, error) {
			result := "（子 Agent 没有给出结果）"
			if len(output.Results) > 0 {
				result = output.Results[len(output.Results)-1]
			}
			return fmt.Sprintf("技能 %s 已由子 Agent 执行完成，结果：\n%s", output.Skill.Name, result), nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create skill middleware: %w", err)
	}
	return middleware, nil
}

// skillCallIDs 返回上下文中加载技能的工具调用编号，这些调用与结果在摘要压缩时原样保留。
func skillCallIDs(messages []*schema.AgenticMessage) map[string]bool {
	ids := make(map[string]bool)
	for _, message := range messages {
		for _, call := range toolCalls(message) {
			if call.Name == skillToolName {
				ids[call.CallID] = true
			}
		}
	}
	return ids
}
