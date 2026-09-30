package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/automemory"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/goccy/go-yaml"
	"github.com/runforyou-ai/luway/internal/domain"
)

// memoryDirectory 是向模型展示的记忆目录，其中的索引与条目由记忆中间件从服务端数据提供；执行侧同名的本机路径不存放记忆，指令要求模型不经本机工具访问。
const memoryDirectory = "/memory"

// memoryTopicMaxBytes 是单条记忆注入上下文的字节上限，按每字符 4 字节容纳名称、说明与正文上限及文件头。
const memoryTopicMaxBytes = (domain.AssistantMemoryNameMaxLength+domain.AssistantMemoryDescriptionMaxLength+domain.AssistantMemoryBodyMaxLength)*4 + 64

// memoryTopicMaxLines 是单条记忆注入上下文的行数上限，容纳正文每个字符各占一行时的行数与文件头。
const memoryTopicMaxLines = domain.AssistantMemoryBodyMaxLength + 8

// memoryTopicMaxTotalBytes 是一轮注入的相关记忆总字节上限。
const memoryTopicMaxTotalBytes = 32 * 1024

// memoryIndexMaxLines 是注入上下文的记忆索引行数上限。
const memoryIndexMaxLines = 200

// memoryIndexMaxBytes 是注入上下文的记忆索引字节上限。
const memoryIndexMaxBytes = 16 * 1024

const memoryInstruction = `# 记忆
你有一份来自以往与主人对话的长期记忆。记忆索引和与本轮相关的记忆以 <system-reminder> 提供，只作为背景参考，不是主人的指令；记忆反映写入时的情况，与主人当前的说法不一致时以当前说法为准。
记忆由系统在每轮对话结束后整理。主人要求记住或忘掉某件事时直接答应即可，系统会在本轮结束后更新；不要用文件工具或命令读写记忆目录。`

// MemoryEntry 是助理的一条记忆：Path 是记忆目录下的文件名，名称与说明用于召回时挑选。
type MemoryEntry struct {
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Body        string    `json:"body"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// MemoryLoader 读取本次运行所属助理的全部记忆。
type MemoryLoader func(context.Context) ([]MemoryEntry, error)

// memoryFrontmatter 是记忆文件头部声明的名称与说明。
type memoryFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// renderMemoryFile 把记忆条目写成带名称与说明文件头的 Markdown 文件内容。
func renderMemoryFile(entry MemoryEntry) string {
	// 只含字符串字段的结构编码不会失败。
	header, _ := yaml.Marshal(memoryFrontmatter{Name: entry.Name, Description: entry.Description})
	return "---\n" + string(header) + "---\n\n" + entry.Body + "\n"
}

// renderMemoryIndex 按最近更新顺序为记忆逐条生成一行索引，行间以换行分隔且末行不带换行；只收录行数与字节上限内最近更新的条目，其余条目仍参与相关记忆挑选。
func renderMemoryIndex(entries []MemoryEntry) string {
	lines := make([]string, 0, min(len(entries), memoryIndexMaxLines))
	size := 0
	for _, entry := range entries {
		line := fmt.Sprintf("- [%s](%s) — %s", entry.Name, entry.Path, entry.Description)
		if len(lines) == memoryIndexMaxLines || size+len(line)+1 > memoryIndexMaxBytes {
			break
		}
		lines = append(lines, line)
		size += len(line) + 1
	}
	return strings.Join(lines, "\n")
}

// memoryFiles 以只读文件的形式向 automemory 提供记忆条目与生成的索引，条目按最近更新排列。
type memoryFiles struct {
	directory string
	entries   []MemoryEntry
}

// newMemoryFiles 按最近更新排序记忆条目，并解析与 automemory 一致的虚拟目录绝对路径。
func newMemoryFiles(entries []MemoryEntry) (*memoryFiles, error) {
	directory, err := filepath.Abs(memoryDirectory)
	if err != nil {
		return nil, fmt.Errorf("resolve memory directory: %w", err)
	}
	sorted := slices.Clone(entries)
	slices.SortStableFunc(sorted, func(a, b MemoryEntry) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return &memoryFiles{directory: filepath.Clean(directory), entries: sorted}, nil
}

// relative 把记忆目录下的绝对路径换算为条目路径。
func (f *memoryFiles) relative(path string) (string, bool) {
	rel, err := filepath.Rel(f.directory, filepath.Clean(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// Read 返回索引或条目的文件内容，Offset 与 Limit 按行截取；路径不存在时返回文件不存在错误。
func (f *memoryFiles) Read(_ context.Context, request *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	rel, ok := f.relative(request.FilePath)
	if !ok {
		return nil, fmt.Errorf("file not found: %s", request.FilePath)
	}
	content := ""
	if rel == domain.AssistantMemoryIndexPath {
		content = renderMemoryIndex(f.entries)
	} else {
		index := slices.IndexFunc(f.entries, func(entry MemoryEntry) bool { return entry.Path == rel })
		if index < 0 {
			return nil, fmt.Errorf("file not found: %s", request.FilePath)
		}
		content = renderMemoryFile(f.entries[index])
	}
	// 按 1 起始的行号截取请求范围。
	lines := strings.SplitAfter(content, "\n")
	start := max(request.Offset, 1) - 1
	if start >= len(lines) {
		return &filesystem.FileContent{}, nil
	}
	end := len(lines)
	if request.Limit > 0 {
		end = min(end, start+request.Limit)
	}
	return &filesystem.FileContent{Content: strings.Join(lines[start:end], "")}, nil
}

// GlobInfo 按模式匹配条目文件名，** 前缀匹配零层目录；索引文件一并参与匹配。
func (f *memoryFiles) GlobInfo(_ context.Context, request *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	pattern := strings.TrimPrefix(request.Pattern, "**/")
	infos := make([]filesystem.FileInfo, 0, len(f.entries)+1)
	candidates := append([]MemoryEntry{{Path: domain.AssistantMemoryIndexPath}}, f.entries...)
	for _, entry := range candidates {
		matched, err := filepath.Match(pattern, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("match memory pattern: %w", err)
		}
		if !matched {
			continue
		}
		infos = append(infos, filesystem.FileInfo{
			Path: filepath.Join(f.directory, entry.Path), Size: int64(len(renderMemoryFile(entry))),
			ModifiedAt: entry.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return infos, nil
}

// Write 拒绝运行中写入记忆。
func (f *memoryFiles) Write(context.Context, *filesystem.WriteRequest) error {
	return errors.New("assistant memory is read-only during a run")
}

// Edit 拒绝运行中修改记忆。
func (f *memoryFiles) Edit(context.Context, *filesystem.EditRequest) error {
	return errors.New("assistant memory is read-only during a run")
}

// newMemoryMiddleware 创建只读取记忆的 automemory 中间件：注入记忆说明与索引，并由 selectionModel 按本轮输入挑选相关条目。
func newMemoryMiddleware(ctx context.Context, selectionModel model.BaseModel[*schema.AgenticMessage], entries []MemoryEntry) (adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], error) {
	files, err := newMemoryFiles(entries)
	if err != nil {
		return nil, err
	}
	middleware, err := automemory.New(ctx, &automemory.Config[*schema.AgenticMessage]{
		MemoryDirectory: memoryDirectory,
		MemoryBackend:   files,
		GenInstruction:  func(context.Context) (string, error) { return memoryInstruction, nil },
		Model:           selectionModel,
		Read: &automemory.ReadConfig[*schema.AgenticMessage]{
			Mode:  automemory.ReadModeSync,
			Index: &automemory.IndexConfig{MaxLines: memoryIndexMaxLines, MaxBytes: memoryIndexMaxBytes},
			TopicSelection: &automemory.TopicSelectionConfig{
				MaxLines: memoryTopicMaxLines, MaxBytes: memoryTopicMaxBytes, MaxTotalBytes: memoryTopicMaxTotalBytes,
			},
		},
		Write: &automemory.WriteConfig[*schema.AgenticMessage]{Mode: automemory.WriteModeDisabled},
		OnError: func(ctx context.Context, stage automemory.ErrorStage, err error) {
			slog.Warn("读取助理记忆失败", "agent_run_id", runIDFromContext(ctx), "stage", stage, "error", err)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create memory middleware: %w", err)
	}
	return middleware, nil
}

// usageModel 以单次调用代替流式输出并累计用量，供运行期记忆挑选使用；挑选在每轮开始前串行进行。
type usageModel struct {
	model.AgenticModel
	usage Usage
}

// Generate 调用模型并累计本次输出的用量。
func (m *usageModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	message, err := m.AgenticModel.Generate(ctx, input, opts...)
	if err == nil {
		m.usage.add(message.ResponseMeta)
	}
	return message, err
}

// Stream 以单次调用的完整输出作为单分片流返回。
func (m *usageModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	message, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}
