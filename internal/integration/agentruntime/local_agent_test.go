package agentruntime

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime/runstream"
)

// fakeLocalAgent 是按预设脚本回应提示的 ACP Agent，记录收到的提示与取消。
type fakeLocalAgent struct {
	conn       *acp.AgentSideConnection
	sessionErr error
	turns      []func(context.Context, acp.SessionId) // 按轮次发送的过程更新。
	blockUntil bool                                   // 为 true 时提示一直等到收到取消。

	mu        sync.Mutex
	prompts   []string
	cancelled chan struct{}
}

// Initialize 返回协议版本。
func (a *fakeLocalAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber}, nil
}

// NewSession 返回预设错误或固定会话。
func (a *fakeLocalAgent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if a.sessionErr != nil {
		return acp.NewSessionResponse{}, a.sessionErr
	}
	return acp.NewSessionResponse{SessionId: "session-1"}, nil
}

// Prompt 记录提示文本并按轮次发送过程更新。
func (a *fakeLocalAgent) Prompt(ctx context.Context, request acp.PromptRequest) (acp.PromptResponse, error) {
	a.mu.Lock()
	a.prompts = append(a.prompts, request.Prompt[0].Text.Text)
	turn := len(a.prompts) - 1
	a.mu.Unlock()
	if a.blockUntil {
		<-a.cancelled
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	if turn < len(a.turns) {
		a.turns[turn](ctx, request.SessionId)
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn, Usage: &acp.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}, nil
}

// Cancel 记录取消。
func (a *fakeLocalAgent) Cancel(context.Context, acp.CancelNotification) error {
	close(a.cancelled)
	return nil
}

// send 发送一条会话更新。
func (a *fakeLocalAgent) send(ctx context.Context, sessionID acp.SessionId, update acp.SessionUpdate) {
	_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sessionID, Update: update})
}

// Authenticate 返回空响应，测试不使用该能力。
func (a *fakeLocalAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

// Logout 返回空响应，测试不使用该能力。
func (a *fakeLocalAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

// CloseSession 返回空响应，测试不使用该能力。
func (a *fakeLocalAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

// ListSessions 返回空响应，测试不使用该能力。
func (a *fakeLocalAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}

// ResumeSession 返回空响应，测试不使用该能力。
func (a *fakeLocalAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

// SetSessionConfigOption 返回空响应，测试不使用该能力。
func (a *fakeLocalAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}

// SetSessionMode 返回空响应，测试不使用该能力。
func (a *fakeLocalAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

// start 返回经内存管道连接该 Agent 的进程启动函数。
func (a *fakeLocalAgent) start() func(context.Context) (LocalAgentProcess, error) {
	return func(context.Context) (LocalAgentProcess, error) {
		clientReader, agentWriter := io.Pipe()
		agentReader, clientWriter := io.Pipe()
		a.cancelled = make(chan struct{})
		a.conn = acp.NewAgentSideConnection(a, agentWriter, agentReader)
		return LocalAgentProcess{Stdin: clientWriter, Stdout: clientReader, Close: func() error {
			_ = clientWriter.Close()
			return agentWriter.Close()
		}}, nil
	}
}

// scriptedFeed 按预设批次提供输入：每批在上一批被认领后才出现。
type scriptedFeed struct {
	mu      sync.Mutex
	batches [][]Message
	claimed int
}

// Peek 返回下一批尚未认领的输入信号。
func (f *scriptedFeed) Peek(_ context.Context, afterSeq int64) ([]Trigger, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimed >= len(f.batches) || int64(f.claimed) != afterSeq {
		return nil, nil
	}
	return []Trigger{{Seq: int64(f.claimed + 1)}}, nil
}

// Claim 认领下一批并返回截至该批的全部上下文消息。
func (f *scriptedFeed) Claim(_ context.Context, throughSeq int64) (ClaimedInput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimed = int(throughSeq)
	var messages []Message
	for _, batch := range f.batches[:f.claimed] {
		messages = append(messages, batch...)
	}
	return ClaimedInput{Messages: messages, EndSeq: throughSeq}, nil
}

// TestRunLocalAgentRecordsProcessAndReply 验证本机 Agent 的思考、说明、工具调用与任务清单进入过程内容，最后一个工具调用后的正文作为回复。
func TestRunLocalAgentRecordsProcessAndReply(t *testing.T) {
	agent := &fakeLocalAgent{}
	agent.turns = []func(context.Context, acp.SessionId){func(ctx context.Context, session acp.SessionId) {
		agent.send(ctx, session, acp.UpdateAgentThoughtText("想一想"))
		agent.send(ctx, session, acp.UpdateAgentMessageText("我先看看"))
		agent.send(ctx, session, acp.StartToolCall("call-1", "Run ls", acp.WithStartKind(acp.ToolKindExecute), acp.WithStartStatus(acp.ToolCallStatusInProgress), acp.WithStartRawInput(map[string]string{"command": "ls"})))
		agent.send(ctx, session, acp.UpdateToolCall("call-1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted), acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock("a.txt"))})))
		agent.send(ctx, session, acp.UpdatePlan(acp.PlanEntry{Content: "列出文件", Status: acp.PlanEntryStatusCompleted}))
		agent.send(ctx, session, acp.UpdateAgentMessageText("文件夹里有 a.txt"))
	}}
	var deltas []runstream.Delta
	result, err := RunLocalAgent(context.Background(), LocalAgentRequest{
		RunID: "run-1", StreamID: "stream-1", Attempt: 1, Assignment: Assignment{Instruction: "你是助理。", LocalAgent: domain.LocalAgentKindCodex},
		Dir: t.TempDir(), Start: agent.start(), OnStream: func(delta runstream.Delta) { deltas = append(deltas, delta) },
	}, &scriptedFeed{batches: [][]Message{{{ID: "m1", Role: MessageRoleUser, Content: "看看文件夹"}}}})
	if err != nil {
		t.Fatalf("RunLocalAgent() error = %v", err)
	}
	if result.Content != "文件夹里有 a.txt" || result.EndSeq != 1 || result.Usage.TotalTokens != 15 {
		t.Fatalf("RunLocalAgent() = %+v", result)
	}
	if len(result.Blocks) != 3 || result.Blocks[0].Kind != domain.AgentRunBlockThinking || result.Blocks[1].Payload.Text != "我先看看" {
		t.Fatalf("blocks = %+v", result.Blocks)
	}
	call := result.Blocks[2].Payload.ToolCall
	if call == nil || call.Name != "execute" || call.Status != domain.AgentToolCallSucceeded || call.Result == nil || *call.Result != "a.txt" || call.Arguments != `{"command":"ls"}` {
		t.Fatalf("tool call = %+v", call)
	}
	if len(result.Plan) != 1 || result.Plan[0].Status != domain.AgentPlanTaskCompleted {
		t.Fatalf("plan = %+v", result.Plan)
	}
	if len(agent.prompts) != 1 || !strings.HasPrefix(agent.prompts[0], "你是助理。") || !strings.Contains(agent.prompts[0], "看看文件夹") {
		t.Fatalf("prompts = %q", agent.prompts)
	}
	if len(deltas) == 0 || deltas[0].BaseSequence != 0 || deltas[len(deltas)-1].Sequence != int64(len(deltas)) {
		t.Fatalf("deltas = %+v", deltas)
	}
}

// TestRunLocalAgentContinuesWithNewInputs 验证一轮结束后仍有新输入时在同一会话继续，新一轮只发送新消息，以最后一轮的回复收尾。
func TestRunLocalAgentContinuesWithNewInputs(t *testing.T) {
	agent := &fakeLocalAgent{}
	agent.turns = []func(context.Context, acp.SessionId){
		func(ctx context.Context, session acp.SessionId) {
			agent.send(ctx, session, acp.UpdateAgentMessageText("第一轮"))
		},
		func(ctx context.Context, session acp.SessionId) {
			agent.send(ctx, session, acp.UpdateAgentMessageText("第二轮"))
		},
	}
	feed := &scriptedFeed{batches: [][]Message{
		{{ID: "m1", Role: MessageRoleUser, Content: "第一个问题"}},
		{{ID: "m2", Role: MessageRoleUser, Content: "补充一句"}},
	}}
	result, err := RunLocalAgent(context.Background(), LocalAgentRequest{
		RunID: "run-1", Assignment: Assignment{Instruction: "你是助理。"}, Dir: t.TempDir(), Start: agent.start(),
	}, feed)
	if err != nil {
		t.Fatalf("RunLocalAgent() error = %v", err)
	}
	if result.Content != "第二轮" || result.EndSeq != 2 || len(result.Blocks) != 1 || result.Blocks[0].Payload.Text != "第一轮" {
		t.Fatalf("RunLocalAgent() = %+v", result)
	}
	if len(agent.prompts) != 2 || strings.Contains(agent.prompts[1], "第一个问题") || !strings.Contains(agent.prompts[1], "补充一句") || strings.Contains(agent.prompts[1], "你是助理。") {
		t.Fatalf("prompts = %q", agent.prompts)
	}
}

// TestRunLocalAgentAuthRequired 验证本机 Agent 要求登录时返回 ErrLocalAgentAuthRequired。
func TestRunLocalAgentAuthRequired(t *testing.T) {
	agent := &fakeLocalAgent{sessionErr: acp.NewAuthRequired(nil)}
	_, err := RunLocalAgent(context.Background(), LocalAgentRequest{RunID: "run-1", Dir: t.TempDir(), Start: agent.start()},
		&scriptedFeed{batches: [][]Message{{{ID: "m1", Role: MessageRoleUser, Content: "你好"}}}})
	if !errors.Is(err, ErrLocalAgentAuthRequired) {
		t.Fatalf("RunLocalAgent() error = %v", err)
	}
}

// TestRunLocalAgentCancelsOnStop 验证运行停止时向本机 Agent 发出取消并返回 context 错误。
func TestRunLocalAgentCancelsOnStop(t *testing.T) {
	agent := &fakeLocalAgent{blockUntil: true}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := RunLocalAgent(ctx, LocalAgentRequest{RunID: "run-1", Dir: t.TempDir(), Start: agent.start()},
		&scriptedFeed{batches: [][]Message{{{ID: "m1", Role: MessageRoleUser, Content: "做个大任务"}}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunLocalAgent() error = %v", err)
	}
	select {
	case <-agent.cancelled:
	default:
		t.Fatal("local agent was not cancelled")
	}
}
