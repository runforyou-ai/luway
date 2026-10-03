package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/localskill"
)

// testSkills 创建以临时目录为技能目录的技能存储，其中已安装带脚本的 xlsx 技能。
func testSkills(t *testing.T) *localskill.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	skill := filepath.Join(dir, "xlsx")
	if err := os.MkdirAll(filepath.Join(skill, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, localskill.FileName), []byte("---\nname: xlsx\ndescription: 处理 Excel 表格\n---\n用 scripts/recalc.py 重算公式"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "scripts", "recalc.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	return localskill.NewStore([]localskill.Dir{{Path: dir, Source: localskill.SourceManaged}}, func() {})
}

// skillChatModel 记录每次带工具调用的输入与工具定义，并按调用次序返回预设输出。
type skillChatModel struct {
	mu      sync.Mutex
	inputs  [][]*schema.AgenticMessage
	tools   [][]*schema.ToolInfo
	outputs func(call int) *schema.AgenticMessage
}

// Generate 记录输入与工具定义并返回本次调用的预设输出。
func (m *skillChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs = append(m.inputs, input)
	m.tools = append(m.tools, model.GetCommonOptions(nil, opts...).Tools)
	return m.outputs(len(m.inputs)), nil
}

// Stream 以单个分片返回本次调用的预设输出。
func (m *skillChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestSkillLoadingSurvivesSummary 验证技能工具列出可用技能、名称不限定枚举，加载结果包含目录与附带文件、重复加载只返回提示，摘要压缩后技能说明原样保留。
func TestSkillLoadingSurvivesSummary(t *testing.T) {
	feed := &testInputFeed{}
	feed.appendUser("把表格公式重算一下")
	long := strings.Repeat("长", 3000)
	main := &skillChatModel{outputs: func(call int) *schema.AgenticMessage {
		switch call {
		case 1:
			return assistantReply("", terminalCall("s1", skillToolName, `{"skill":"xlsx"}`))
		case 2:
			return assistantReply("", terminalCall("s2", skillToolName, `{"skill":"xlsx"}`))
		case 3:
			feed.appendUser("再检查一遍")
			return assistantReply(long)
		}
		return assistantReply("检查完了")
	}}
	chatModel := &summaryModel{AgenticModel: main}
	skills := testSkills(t)
	listed, err := skills.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	computer := &fakeComputer{skills: skills}
	runtime := &EinoRuntime{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runtime.Run(ctx, RunRequest{
		RunID: "skill-run", Assignment: Assignment{AgentName: "小码", Tools: []string{skillToolName}, Model: AssignmentModel{ContextWindow: 4000}},
		Models: fixedModels(chatModel), Computer: computer, ComputerCapabilities: domain.ComputerCapabilities{Skills: computerSkills(listed)}, MaxTurns: 2,
	}, feed)
	if err != nil || result.Content != "检查完了" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	// 技能工具的说明列出可用技能，技能名称限定为枚举。
	index := slices.IndexFunc(main.tools[0], func(info *schema.ToolInfo) bool { return info.Name == skillToolName })
	if index < 0 || !strings.Contains(main.tools[0][index].Desc, "- xlsx：处理 Excel 表格") {
		t.Fatalf("skill tool = %+v", main.tools[0])
	}
	parameters, err := main.tools[0][index].ParamsOneOf.ToJSONSchema()
	if err != nil || len(parameters.Properties.Value("skill").Enum) != 0 {
		t.Fatalf("skill parameters = %+v, err = %v", parameters, err)
	}
	loaded := messageText(main.inputs[1][len(main.inputs[1])-1])
	if !strings.Contains(loaded, `<skill_content name="xlsx">`) || !strings.Contains(loaded, "<file>scripts/recalc.py</file>") ||
		strings.Contains(loaded, "<file>SKILL.md</file>") || !strings.Contains(loaded, "用 scripts/recalc.py 重算公式") {
		t.Fatalf("loaded skill = %q", loaded)
	}
	if repeated := messageText(main.inputs[2][len(main.inputs[2])-1]); !strings.Contains(repeated, "已在上文加载") {
		t.Fatalf("repeated skill = %q", repeated)
	}
	if len(chatModel.summaries) != 1 {
		t.Fatalf("summaries = %d", len(chatModel.summaries))
	}
	got := conversation(main.inputs[3])
	if len(got) < 3 || !strings.HasPrefix(got[0], summaryPreamble) || got[len(got)-1] != "再检查一遍" ||
		!slices.ContainsFunc(got, func(text string) bool { return strings.Contains(text, `<skill_content name="xlsx">`) }) {
		t.Fatalf("second turn input = %q", got)
	}
}

// TestSkillBackendLoadsFromComputer 验证技能目录取自电脑上报的能力，加载经电脑读取且不挂起运行，名称不存在时列出可用技能，托管运行环境下补充依赖安装方式。
func TestSkillBackendLoadsFromComputer(t *testing.T) {
	skills := testSkills(t)
	listed, err := skills.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	computer := &fakeComputer{skills: skills}
	backend := &skillBackend{computer: computer, skills: computerSkills(listed), managed: true, loaded: make(map[string][32]byte), files: make(map[string][]string)}
	ctx := context.WithValue(context.Background(), toolCallContextKey{}, toolCallMetadata{CallID: "s1", RecordID: "r1", Suspendable: true})
	loaded, err := backend.Get(ctx, "xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if len(computer.suspends) != 1 || computer.suspends[0] {
		t.Fatalf("suspends = %v", computer.suspends)
	}
	if content, _ := backend.content(ctx, loaded, `{"skill":"xlsx"}`); !strings.Contains(content, "uv run --with") || !strings.Contains(content, "<file>scripts/recalc.py</file>") {
		t.Fatalf("content = %q", content)
	}
	if _, err := backend.Get(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "xlsx") {
		t.Fatalf("未知技能的错误应列出可用技能: %v", err)
	}
}

// computerSkills 把技能存储中的技能转换为电脑上报的技能目录。
func computerSkills(skills []localskill.Skill) []domain.ComputerSkill {
	output := make([]domain.ComputerSkill, 0, len(skills))
	for _, skill := range skills {
		output = append(output, domain.ComputerSkill{Name: skill.Name, Description: skill.Description, Dir: skill.Dir, Fork: skill.Fork})
	}
	return output
}
