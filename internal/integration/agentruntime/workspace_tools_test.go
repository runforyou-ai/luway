package agentruntime

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestResolveAssignmentLocalTools 验证本机工具进入工具清单并在指令中说明，没有本机工具时不出现说明。
func TestResolveAssignmentLocalTools(t *testing.T) {
	facts := AssignmentFacts{OrganizationName: "测试企业", AgentName: "小码", Scene: SceneContext{Scene: SceneAgentChat}}
	assignment := ResolveAssignment(facts, Capabilities{LocalTools: []string{"ls", "read_file"}})
	if !slices.Contains(assignment.Tools, "ls") || !slices.Contains(assignment.Tools, "read_file") || !strings.Contains(assignment.Instruction, "- ls、read_file：在这台电脑上查阅和修改文件、运行命令") {
		t.Fatalf("assignment=%+v", assignment)
	}
	plain := ResolveAssignment(facts, Capabilities{})
	if slices.ContainsFunc(plain.Tools, IsLocalTool) || strings.Contains(plain.Instruction, "在这台电脑上") {
		t.Fatalf("assignment without workspace=%+v", plain)
	}
}

// imageWorkspace 是返回固定文本与图片的测试本机文件后端，按图片读取时图片文件返回图片内容，并记录删除与命令调用。
type imageWorkspace struct {
	filesystem.Backend
	mu       sync.Mutex
	deleted  []string
	commands []string
}

// Delete 记录删除的路径。
func (w *imageWorkspace) Delete(_ context.Context, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deleted = append(w.deleted, path)
	return nil
}

// Execute 记录执行的命令并返回固定输出。
func (w *imageWorkspace) Execute(_ context.Context, req *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.commands = append(w.commands, req.Command)
	exitCode := 0
	return &filesystem.ExecuteResponse{Output: "done", ExitCode: &exitCode}, nil
}

// Read 以文本返回任意文件。
func (*imageWorkspace) Read(context.Context, *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	return &filesystem.FileContent{Content: "text view"}, nil
}

// MultiModalRead 以图片返回任意文件。
func (*imageWorkspace) MultiModalRead(context.Context, *filesystem.MultiModalReadRequest) (*filesystem.MultiFileContent, error) {
	return &filesystem.MultiFileContent{Parts: []filesystem.FileContentPart{{Type: filesystem.FileContentPartTypeImage, MIMEType: "image/png", Data: []byte("png")}}}, nil
}

// workspaceChatModel 第一次调用读取本机文件，拿到工具结果后给出回答；rejectImages 为 true 时拒绝携带工具图片的请求。
type workspaceChatModel struct {
	mu           sync.Mutex
	rejectImages bool
	tools        [][]string
	inputs       [][]*schema.AgenticMessage
}

// Generate 按输入中是否已有工具结果决定调用工具或回答。
func (m *workspaceChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for _, info := range model.GetCommonOptions(nil, opts...).Tools {
		names = append(names, info.Name)
	}
	m.tools = append(m.tools, names)
	m.inputs = append(m.inputs, input)
	if m.rejectImages && carriesMedia(input) {
		return nil, errors.New("tool result images are not supported")
	}
	if toolResult(input[len(input)-1]) == nil {
		return assistantReply("", &schema.FunctionToolCall{CallID: "read-1", Name: "read_file", Arguments: `{"file_path":"/logo.png"}`}), nil
	}
	return assistantReply("已读取"), nil
}

// Stream 以单个分片返回模型输出。
func (m *workspaceChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// runWorkspace 以只含 ls 与 read_file 的有效配置执行一次运行。
func runWorkspace(t *testing.T, chatModel *workspaceChatModel, modalities []domain.AIModelInputModality) RunResult {
	t.Helper()
	runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
	feed := &testInputFeed{}
	feed.appendUser("看看 logo")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := runtime.Run(ctx, RunRequest{
		RunID:      "workspace-run",
		Assignment: Assignment{AgentName: "小码", Tools: []string{"ls", "read_file"}, Model: AssignmentModel{InputModalities: modalities}},
		Workspace:  &imageWorkspace{Backend: filesystem.NewInMemoryBackend()},
	}, feed)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestWorkspaceToolsFollowAssignment 验证只注册有效配置列出的本机工具，模型不支持图片时按文本读取。
func TestWorkspaceToolsFollowAssignment(t *testing.T) {
	chatModel := &workspaceChatModel{}
	result := runWorkspace(t, chatModel, nil)
	if result.Content != "已读取" {
		t.Fatalf("result=%+v", result)
	}
	tools := chatModel.tools[0]
	if !slices.Contains(tools, "ls") || !slices.Contains(tools, "read_file") || slices.Contains(tools, "grep") || slices.Contains(tools, "glob") || slices.Contains(tools, "write_file") {
		t.Fatalf("registered tools=%v", tools)
	}
	if text := messageText(chatModel.inputs[1][len(chatModel.inputs[1])-1]); !strings.Contains(text, "text view") {
		t.Fatalf("tool result=%q", text)
	}
	if _, err := newWorkspaceTools(context.Background(), RunRequest{Assignment: Assignment{Tools: []string{"ls"}}}, nil, nil); err == nil {
		t.Fatal("workspace tools created without workspace")
	}
}

// TestWorkspaceImageRead 验证模型支持图片时本机图片随工具结果交给模型并以类型记入过程；模型拒绝后去掉图片只重试模型调用，不重新读取文件。
func TestWorkspaceImageRead(t *testing.T) {
	images := []domain.AIModelInputModality{domain.AIModelInputModalityText, domain.AIModelInputModalityImage}
	chatModel := &workspaceChatModel{}
	result := runWorkspace(t, chatModel, images)
	if !carriesMedia(chatModel.inputs[1]) {
		t.Fatal("image result was not passed to model")
	}
	recorded := ""
	for _, block := range result.Blocks {
		if call := block.Payload.ToolCall; call != nil && call.Result != nil {
			recorded = *call.Result
		}
	}
	if recorded != "[image image/png]" {
		t.Fatalf("recorded result=%q", recorded)
	}

	rejecting := &workspaceChatModel{rejectImages: true}
	if result := runWorkspace(t, rejecting, images); result.Content != "已读取" {
		t.Fatalf("result after rejection=%+v", result)
	}
	last := rejecting.inputs[len(rejecting.inputs)-1]
	if len(rejecting.inputs) != 3 || carriesMedia(last) || !strings.Contains(messageText(last[len(last)-1]), "当前模型无法查看此内容") {
		t.Fatalf("model calls=%d, retry input=%v", len(rejecting.inputs), messageText(last[len(last)-1]))
	}
}

// sideEffectChatModel 第一次调用删除文件并执行命令，之后先返回一次空正文再给出回答。
type sideEffectChatModel struct {
	mu    sync.Mutex
	calls int
	tools []string
}

// Generate 按调用次数返回工具调用、空正文或回答。
func (m *sideEffectChatModel) Generate(_ context.Context, _ []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		for _, info := range model.GetCommonOptions(nil, opts...).Tools {
			m.tools = append(m.tools, info.Name)
		}
		return assistantReply("",
			&schema.FunctionToolCall{CallID: "delete-1", Name: "delete_file", Arguments: `{"file_path":"old.txt"}`},
			&schema.FunctionToolCall{CallID: "execute-1", Name: "execute", Arguments: `{"command":"echo hi"}`}), nil
	}
	if m.calls == 2 {
		return assistantReply(""), nil
	}
	return assistantReply("已完成"), nil
}

// Stream 以单个分片返回模型输出。
func (m *sideEffectChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestWorkspaceSideEffectsRunOnce 验证本机改动类工具按有效配置注册，模型空正文重试时已执行的删除与命令不重复执行。
func TestWorkspaceSideEffectsRunOnce(t *testing.T) {
	chatModel := &sideEffectChatModel{}
	workspace := &imageWorkspace{Backend: filesystem.NewInMemoryBackend()}
	runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
	feed := &testInputFeed{}
	feed.appendUser("清理一下")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := runtime.Run(ctx, RunRequest{
		RunID: "workspace-side-effects", Assignment: Assignment{AgentName: "小码", Tools: LocalTools()}, Workspace: workspace,
		LocalMCP: &stubLocalMCP{}, Skills: testSkills(t),
	}, feed)
	if err != nil || result.Content != "已完成" {
		t.Fatalf("result=%+v, err=%v", result, err)
	}
	for _, name := range LocalTools() {
		if !slices.Contains(chatModel.tools, name) {
			t.Fatalf("registered tools=%v, missing %s", chatModel.tools, name)
		}
	}
	if chatModel.calls != 3 || !slices.Equal(workspace.deleted, []string{"old.txt"}) || !slices.Equal(workspace.commands, []string{"echo hi"}) {
		t.Fatalf("calls=%d, deleted=%v, commands=%v", chatModel.calls, workspace.deleted, workspace.commands)
	}
}

// TestExecuteToolDescribesManagedToolchain 验证执行设备提供托管运行环境时命令工具说明补充用法，未提供时不提及。
func TestExecuteToolDescribesManagedToolchain(t *testing.T) {
	for _, managed := range []bool{true, false} {
		chatModel := &customerHistoryChatModel{processChatModel: &processChatModel{generate: func(context.Context, []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
			return assistantReply("完成"), nil
		}}}
		runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
		feed := &testInputFeed{}
		feed.appendUser("运行脚本")
		_, err := runtime.Run(context.Background(), RunRequest{
			RunID: "managed-toolchain", Assignment: Assignment{AgentName: "小码", Tools: []string{"execute"}},
			Workspace: &imageWorkspace{Backend: filesystem.NewInMemoryBackend()}, ManagedToolchain: managed,
		}, feed)
		if err != nil {
			t.Fatal(err)
		}
		desc := ""
		for _, info := range chatModel.tools {
			if info.Name == "execute" {
				desc = info.Desc
			}
		}
		if desc == "" || strings.Contains(desc, "uv run --with") != managed || strings.Contains(desc, "uv run python") != managed || strings.Contains(desc, "--break-system-packages") != managed {
			t.Fatalf("managed=%v 时命令工具说明不符合预期:\n%s", managed, desc)
		}
	}
}
