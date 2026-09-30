package agentruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
)

// testMemoryEntries 构造两条更新时间不同的记忆。
func testMemoryEntries() []MemoryEntry {
	now := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	return []MemoryEntry{
		{Path: "role.md", Name: "主人的角色", Description: "主人负责华东区销售", Body: "主人是华东区销售负责人。", UpdatedAt: now.Add(-time.Hour)},
		{Path: "work-style.md", Name: "工作方式", Description: "主人希望先给结论", Body: "回答先给结论，再列依据。", UpdatedAt: now},
	}
}

// TestMemoryFiles 验证记忆条目以带文件头的只读文件提供，索引按最近更新生成。
func TestMemoryFiles(t *testing.T) {
	files, err := newMemoryFiles(testMemoryEntries())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	index, err := files.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(files.directory, "MEMORY.md")})
	if err != nil {
		t.Fatal(err)
	}
	if index.Content != "- [工作方式](work-style.md) — 主人希望先给结论\n- [主人的角色](role.md) — 主人负责华东区销售" {
		t.Fatalf("索引 = %q", index.Content)
	}
	file, err := files.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(files.directory, "role.md")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(file.Content, "---\nname: 主人的角色\ndescription: 主人负责华东区销售\n---\n\n主人是华东区销售负责人。") {
		t.Fatalf("记忆文件 = %q", file.Content)
	}
	preview, err := files.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(files.directory, "role.md"), Offset: 2, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Content != "name: 主人的角色\ndescription: 主人负责华东区销售\n" {
		t.Fatalf("按行截取 = %q", preview.Content)
	}
	if _, err := files.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(files.directory, "missing.md")}); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("不存在的记忆应返回文件不存在：%v", err)
	}
	infos, err := files.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "**/*.md", Path: files.directory})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, info := range infos {
		names = append(names, filepath.Base(info.Path))
	}
	if !slices.Equal(names, []string{"MEMORY.md", "work-style.md", "role.md"}) {
		t.Fatalf("匹配结果 = %v", names)
	}
	if err := files.Write(ctx, &filesystem.WriteRequest{FilePath: filepath.Join(files.directory, "new.md"), Content: "x"}); err == nil {
		t.Fatal("运行中写入记忆应被拒绝")
	}
	// 条目超出索引上限时只收录最近更新的条目，索引不超过注入上限。
	many := make([]MemoryEntry, 0, memoryIndexMaxLines+50)
	for i := range memoryIndexMaxLines + 50 {
		many = append(many, MemoryEntry{Path: fmt.Sprintf("m%03d.md", i), Name: strings.Repeat("名", 20), Description: strings.Repeat("说", 60), UpdatedAt: time.Unix(int64(i), 0)})
	}
	crowded, err := newMemoryFiles(many)
	if err != nil {
		t.Fatal(err)
	}
	index, err = crowded.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(crowded.directory, "MEMORY.md")})
	if err != nil {
		t.Fatal(err)
	}
	// 按 automemory 的算法数行，索引不应触发其截断。
	if lines := len(strings.Split(index.Content, "\n")); len(index.Content) > memoryIndexMaxBytes || lines > memoryIndexMaxLines || !strings.HasPrefix(index.Content, "- ["+many[len(many)-1].Name+"](m249.md)") {
		t.Fatalf("索引长度 %d 字节 %d 行，开头 %q", len(index.Content), lines, index.Content[:40])
	}
	// 短行时按行数上限收录，恰好 200 行。
	short := make([]MemoryEntry, 0, memoryIndexMaxLines+50)
	for i := range memoryIndexMaxLines + 50 {
		short = append(short, MemoryEntry{Path: fmt.Sprintf("s%03d.md", i), Name: "名", Description: "说", UpdatedAt: time.Unix(int64(i), 0)})
	}
	if lines := len(strings.Split(renderMemoryIndex(short), "\n")); lines != memoryIndexMaxLines {
		t.Fatalf("短行索引行数 = %d", lines)
	}
}

// TestMemoryTopicLimits 验证写到上限的记忆不会触发主题记忆的截断提示。
func TestMemoryTopicLimits(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("a\n", domain.AssistantMemoryBodyMaxLength/2),
		strings.Repeat("😀", domain.AssistantMemoryBodyMaxLength),
	} {
		content := renderMemoryFile(MemoryEntry{
			Path: "full.md", Name: strings.Repeat("😀", domain.AssistantMemoryNameMaxLength),
			Description: strings.Repeat("😀", domain.AssistantMemoryDescriptionMaxLength), Body: body,
		})
		if lines := len(strings.Split(content, "\n")); lines > memoryTopicMaxLines || len(content) > memoryTopicMaxBytes {
			t.Fatalf("满额记忆 %d 行 %d 字节超出上限 %d 行 %d 字节", lines, len(content), memoryTopicMaxLines, memoryTopicMaxBytes)
		}
	}
}

// memoryRunModel 记录主模型输入，并为关闭思考的挑选模型选中工作方式记忆。
type memoryRunModel struct {
	mu        sync.Mutex
	selection bool
	mainInput *[]*schema.AgenticMessage
}

// Generate 挑选模型选中 work-style.md，主模型记录输入后直接回复。
func (m *memoryRunModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	if m.selection {
		selected := assistantReply("", &schema.FunctionToolCall{CallID: "select-1", Name: "select_memories", Arguments: `{"selected_memories":["work-style.md"]}`})
		selected.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{PromptTokens: 30, CompletionTokens: 5, TotalTokens: 35}}
		return selected, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	*m.mainInput = input
	return assistantReply("好的"), nil
}

// Stream 以单个分片返回模型输出。
func (m *memoryRunModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestEinoRuntimeInjectsMemory 验证启用记忆的运行注入记忆说明、索引与挑选出的相关条目。
func TestEinoRuntimeInjectsMemory(t *testing.T) {
	var mainInput []*schema.AgenticMessage
	runtime := &EinoRuntime{newModel: func(_ context.Context, config ModelConfig) (model.AgenticModel, error) {
		return &memoryRunModel{selection: config.DisableThinking, mainInput: &mainInput}, nil
	}}
	feed := &testInputFeed{}
	feed.appendUser("帮我整理本周的销售周报")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request := RunRequest{
		RunID: "memory-run", Assignment: Assignment{AgentName: "小码", Scene: SceneAgentChat, Memory: true}, MaxTurns: 1,
		Memory: func(context.Context) ([]MemoryEntry, error) { return testMemoryEntries(), nil },
	}
	result, err := runtime.Run(ctx, request, feed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.TotalTokens != 35 {
		t.Fatalf("运行用量未包含记忆挑选：%+v", result.Usage)
	}
	var system, reminders strings.Builder
	for _, message := range mainInput {
		if message.Role == schema.AgenticRoleTypeSystem {
			for _, block := range message.ContentBlocks {
				if block.Type == schema.ContentBlockTypeUserInputText {
					system.WriteString(block.UserInputText.Text)
				}
			}
			continue
		}
		reminders.WriteString(messageText(message))
	}
	if !strings.Contains(system.String(), "你有一份来自以往与主人对话的长期记忆") {
		t.Fatalf("系统指令未包含记忆说明：%q", system.String())
	}
	if !strings.Contains(reminders.String(), "- [工作方式](work-style.md) — 主人希望先给结论") {
		t.Fatalf("未注入记忆索引：%q", reminders.String())
	}
	if !strings.Contains(reminders.String(), "回答先给结论，再列依据。") || strings.Contains(reminders.String(), "主人是华东区销售负责人。") {
		t.Fatalf("相关记忆注入不正确：%q", reminders.String())
	}
}

// extractionModel 按脚本依次返回提取 Agent 的模型输出，并记录首次调用的输入与之后的工具结果。
type extractionModel struct {
	mu      sync.Mutex
	calls   int
	input   string
	results []string
}

// Generate 首次调用保存、删除并尝试保存一条非法路径的记忆，第二次调用结束提取。
func (m *extractionModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		m.input = messageText(input[len(input)-1])
		return assistantReply("",
			&schema.FunctionToolCall{CallID: "save-1", Name: "save_memory", Arguments: `{"path":"report.md","name":"周报格式","description":"主人要求周报按客户分组","body":"周报按客户分组，每组列出进展与风险。"}`},
			&schema.FunctionToolCall{CallID: "delete-1", Name: "delete_memory", Arguments: `{"path":"role.md"}`},
			&schema.FunctionToolCall{CallID: "save-2", Name: "save_memory", Arguments: `{"path":"MEMORY.md","name":"索引","description":"索引","body":"x"}`},
		), nil
	}
	for _, message := range input {
		if result := toolResult(message); result != nil {
			m.results = append(m.results, messageText(message))
		}
	}
	return assistantReply("完成"), nil
}

// Stream 以单个分片返回模型输出。
func (m *extractionModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestExtractMemory 验证提取 Agent 的输入包含现有记忆与新消息，保存与删除以变更返回，不合规的条目不保存。
func TestExtractMemory(t *testing.T) {
	chatModel := &extractionModel{}
	runtime := &EinoRuntime{newModel: func(_ context.Context, config ModelConfig) (model.AgenticModel, error) {
		if !config.DisableThinking {
			t.Error("记忆提取应关闭思考")
		}
		return chatModel, nil
	}}
	result, err := runtime.ExtractMemory(context.Background(), MemoryExtractionRequest{
		Entries: testMemoryEntries(),
		Earlier: []MemoryMessage{{Sender: "owner", Content: "你好"}},
		Recent:  []MemoryMessage{{Sender: "owner", Content: "以后周报按客户分组"}, {Sender: "assistant", Content: "好的"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"- work-style.md：工作方式 — 主人希望先给结论", "## 此前的消息\n[{\"sender\":\"owner\",\"content\":\"你好\"}]", "以后周报按客户分组"} {
		if !strings.Contains(chatModel.input, expected) {
			t.Fatalf("提取输入缺少 %q：%s", expected, chatModel.input)
		}
	}
	if len(result.Saved) != 1 || result.Saved[0].Path != "report.md" || result.Saved[0].Name != "周报格式" {
		t.Fatalf("保存的记忆 = %#v", result.Saved)
	}
	if !slices.Equal(result.Deleted, []string{"role.md"}) {
		t.Fatalf("删除的记忆 = %v", result.Deleted)
	}
	if !slices.ContainsFunc(chatModel.results, func(text string) bool { return strings.HasPrefix(text, "未保存：path") }) {
		t.Fatalf("非法路径应以结果提示模型：%v", chatModel.results)
	}
}

// TestResolveAssignmentMemory 验证记忆只在执行侧支持且为 AI 单聊时启用。
func TestResolveAssignmentMemory(t *testing.T) {
	facts := customerFacts()
	facts.Scene = SceneContext{Scene: SceneAgentChat}
	if !ResolveAssignment(facts, Capabilities{Memory: true}).Memory {
		t.Fatal("助理单聊应启用记忆")
	}
	if ResolveAssignment(facts, Capabilities{}).Memory {
		t.Fatal("执行侧不支持时不应启用记忆")
	}
	facts.Scene = SceneContext{Scene: SceneGroup}
	if ResolveAssignment(facts, Capabilities{Memory: true}).Memory {
		t.Fatal("群聊不应启用记忆")
	}
}
