//go:build !server && !ios && !android

package executor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/executor/localagent"
	"github.com/runforyou-ai/luway/internal/executor/localworkspace"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
)

const (
	// localAgentStartTimeout 是启动本机 Agent 并建立 ACP 会话的时限，首次经 npx 启动时包含下载适配器的时间。
	localAgentStartTimeout = 3 * time.Minute
	// localAgentStderrBytes 是启动失败时附在错误中的本机 Agent 标准错误输出的末尾字节数。
	localAgentStderrBytes = 2 << 10
	// acpAuthRequired 是 ACP 要求先登录的错误码。
	acpAuthRequired = -32000
	// localAgentIdleTimeout 是支持恢复会话的本机 Agent 没有执行中的一轮时保留进程的时长，超过后关闭进程，下一轮重新启动并恢复 ACP 会话。
	localAgentIdleTimeout = 30 * time.Minute
)

// turnSink 接收本机 Agent 一轮执行中的过程更新与权限请求。
type turnSink interface {
	// update 记录一条过程更新，调用方按顺序上报。
	update(domain.ToolCallUpdate)
	// permission 提交权限请求并等待裁决，返回选用的处理方式编号，为空表示请求已取消；ctx 结束时返回错误。
	permission(ctx context.Context, request domain.LocalAgentPermission) (string, error)
}

// localAgentPool 持有这台电脑上进行中的本机 Agent 会话，按服务端的本机 Agent 会话编号索引；每个会话运行一个本机 Agent 进程，支持恢复会话的进程空闲超过 idle 时关闭。
type localAgentPool struct {
	mu       sync.Mutex
	sessions map[string]*localAgentSession
	idle     time.Duration
}

// newLocalAgentPool 创建空的本机 Agent 会话池。
func newLocalAgentPool() *localAgentPool {
	return &localAgentPool{sessions: map[string]*localAgentSession{}, idle: localAgentIdleTimeout}
}

// held 返回持有的本机 Agent 会话编号。
func (p *localAgentPool) held() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Collect(maps.Keys(p.sessions))
}

// release 关闭指定的本机 Agent 会话并结束其进程。
func (p *localAgentPool) release(ids ...string) {
	p.mu.Lock()
	sessions := make([]*localAgentSession, 0, len(ids))
	for _, id := range ids {
		if session := p.sessions[id]; session != nil {
			sessions = append(sessions, session)
			delete(p.sessions, id)
		}
	}
	p.mu.Unlock()
	for _, session := range sessions {
		slog.InfoContext(context.Background(), "关闭本机 Agent 会话", "session_id", session.id, "local_agent", session.agent)
		session.close()
	}
}

// drop 在会话仍是该编号当前登记的会话时移除它，并关闭它。
func (p *localAgentPool) drop(session *localAgentSession) {
	p.mu.Lock()
	if p.sessions[session.id] == session {
		delete(p.sessions, session.id)
	}
	p.mu.Unlock()
	session.close()
}

// close 关闭全部本机 Agent 会话。
func (p *localAgentPool) close() {
	p.release(p.held()...)
}

// run 在本机 Agent 会话中执行一轮：会话尚未启动或进程已退出时启动本机 Agent，恢复此前的 ACP 会话或新建会话，随后发送提示并把过程交给 sink，返回最终回复与 ACP 会话编号；
// 同一会话的各轮依次执行，ctx 结束时请求本机 Agent 取消这一轮。
func (p *localAgentPool) run(ctx context.Context, agent localagent.Agent, environment localworkspace.Environment, dir string, operation domain.ComputerOperation, sink turnSink) (domain.ComputerOutcome, error) {
	session, err := p.open(ctx, agent, environment, dir, operation.Session, acp.SessionId(operation.AgentSession))
	if err != nil {
		return domain.ComputerOutcome{}, err
	}
	defer p.leave(session)
	session.turn.Lock()
	defer session.turn.Unlock()
	session.client.begin(sink)
	response, err := session.conn.Prompt(ctx, acp.PromptRequest{SessionId: session.acpSession, Prompt: []acp.ContentBlock{acp.TextBlock(operation.Prompt)}})
	reply := session.client.finish()
	select {
	case <-session.conn.Done():
		p.drop(session)
		return domain.ComputerOutcome{}, errors.New("本机 Agent 已退出，这一轮没有完成。")
	default:
	}
	if err != nil {
		if ctx.Err() != nil {
			return domain.ComputerOutcome{}, ctx.Err()
		}
		return domain.ComputerOutcome{}, fmt.Errorf("本机 Agent 执行失败：%w", err)
	}
	if response.StopReason == acp.StopReasonCancelled {
		return domain.ComputerOutcome{}, errors.New("本机 Agent 取消了这一轮。")
	}
	return domain.ComputerOutcome{Output: reply, AgentSession: string(session.acpSession)}, nil
}

// open 返回指定编号的本机 Agent 会话并登记一轮使用，没有或进程已退出时启动本机 Agent、完成初始化并恢复 previous 指定的 ACP 会话或新建会话；调用方用完后调用 leave。
func (p *localAgentPool) open(ctx context.Context, agent localagent.Agent, environment localworkspace.Environment, dir, id string, previous acp.SessionId) (*localAgentSession, error) {
	if session := p.enter(id); session != nil {
		return session, nil
	}
	session, err := startLocalAgentSession(ctx, agent, environment, dir, id, previous)
	if err != nil {
		return nil, err
	}
	// 同一会话同时启动时保留先登记的进程。
	if existing := p.enter(id); existing != nil {
		go session.close()
		return existing, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	session.users = 1
	p.sessions[id] = session
	return session, nil
}

// enter 为进程仍在运行的已登记会话登记一轮使用并停止空闲计时，进程已退出时移除并关闭它；没有可用会话时返回 nil。
func (p *localAgentPool) enter(id string) *localAgentSession {
	p.mu.Lock()
	session := p.sessions[id]
	if session == nil {
		p.mu.Unlock()
		return nil
	}
	select {
	case <-session.conn.Done():
		delete(p.sessions, id)
		p.mu.Unlock()
		session.close()
		return nil
	default:
	}
	defer p.mu.Unlock()
	session.users++
	if session.idle != nil {
		session.idle.Stop()
		session.idle = nil
	}
	return session
}

// leave 结束一轮使用；本机 Agent 支持恢复会话、会话仍登记且没有其他使用时开始空闲计时，计时结束仍无使用则移除并关闭会话。
func (p *localAgentPool) leave(session *localAgentSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	session.users--
	if session.users > 0 || !session.resumable || p.sessions[session.id] != session {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(p.idle, func() {
		p.mu.Lock()
		expired := p.sessions[session.id] == session && session.idle == timer
		if expired {
			delete(p.sessions, session.id)
		}
		p.mu.Unlock()
		if expired {
			slog.InfoContext(context.Background(), "本机 Agent 会话空闲，关闭进程", "session_id", session.id, "local_agent", session.agent)
			session.close()
		}
	})
	session.idle = timer
}

// localAgentSession 是一个运行中的本机 Agent 进程及其 ACP 会话。
type localAgentSession struct {
	id         string
	agent      string
	process    *localworkspace.Process
	conn       *acp.ClientSideConnection
	client     *acpClient
	acpSession acp.SessionId
	// turn 让同一会话的各轮依次执行。
	turn sync.Mutex
	// resumable 表示本机 Agent 支持恢复会话，进程关闭后可以恢复同一 ACP 会话。
	resumable bool
	// users 是正在使用会话的轮数，idle 是没有使用时的空闲计时，二者由会话池的锁保护。
	users int
	idle  *time.Timer
}

// startLocalAgentSession 在会话默认文件夹中启动本机 Agent 并完成 ACP 初始化：previous 非空且本机 Agent 支持恢复会话时恢复该会话，恢复失败或不支持时新建会话；
// 恢复时本机 Agent 重放的历史不在一轮中，不上报。本机 Agent 使用自身的文件与命令工具，不使用这台电脑提供的文件与终端能力。
func startLocalAgentSession(ctx context.Context, agent localagent.Agent, environment localworkspace.Environment, dir, id string, previous acp.SessionId) (*localAgentSession, error) {
	// 本机 Agent 及其经 npx 启动的适配器不输出 npm 安装摘要与提示，标准输出只作协议通道。
	variables := append(slices.Clone(environment.Variables), "NPM_CONFIG_LOGLEVEL=silent", "NPM_CONFIG_FUND=false", "NPM_CONFIG_UPDATE_NOTIFIER=false")
	for name, value := range agent.Env {
		variables = append(variables, name+"="+value)
	}
	environment.Variables = variables
	stderr := &tailBuffer{limit: localAgentStderrBytes}
	process, err := localworkspace.StartProcess(context.Background(), environment, dir, stderr, agent.Command, agent.Args...)
	if err != nil {
		return nil, fmt.Errorf("启动本机 Agent %s 失败：%w", agent.Name, err)
	}
	client := &acpClient{}
	conn := acp.NewClientSideConnection(client, process.Stdin, process.Stdout)
	session := &localAgentSession{id: id, agent: agent.Name, process: process, conn: conn, client: client}
	startCtx, cancel := context.WithTimeout(ctx, localAgentStartTimeout)
	defer cancel()
	initialized, err := conn.Initialize(startCtx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	if err != nil {
		session.close()
		return nil, startError(agent.Name, err, stderr)
	}
	session.resumable = initialized.AgentCapabilities.LoadSession
	if previous != "" && initialized.AgentCapabilities.LoadSession {
		_, err := conn.LoadSession(startCtx, acp.LoadSessionRequest{SessionId: previous, Cwd: dir, McpServers: []acp.McpServer{}})
		if err == nil {
			session.acpSession = previous
			slog.InfoContext(ctx, "本机 Agent 会话已恢复", "session_id", id, "local_agent", agent.Name)
			return session, nil
		}
		slog.WarnContext(ctx, "恢复本机 Agent 会话失败，新建会话", "session_id", id, "local_agent", agent.Name, "error", err)
	}
	created, err := conn.NewSession(startCtx, acp.NewSessionRequest{Cwd: dir, McpServers: []acp.McpServer{}})
	if err != nil {
		session.close()
		return nil, startError(agent.Name, err, stderr)
	}
	session.acpSession = created.SessionId
	slog.InfoContext(ctx, "本机 Agent 会话已建立", "session_id", id, "local_agent", agent.Name)
	return session, nil
}

// startError 返回启动本机 Agent 失败时交给用户的原因：需要登录时提示先在电脑上登录，其余附上本机 Agent 标准错误输出的末尾。
func startError(name string, err error, stderr *tailBuffer) error {
	var requestError *acp.RequestError
	if errors.As(err, &requestError) && requestError.Code == acpAuthRequired {
		return fmt.Errorf("本机 Agent %s 需要先在这台电脑上登录。", name)
	}
	if tail := strings.TrimSpace(stderr.String()); tail != "" {
		return fmt.Errorf("启动本机 Agent %s 失败：%w\n%s", name, err, tail)
	}
	return fmt.Errorf("启动本机 Agent %s 失败：%w", name, err)
}

// close 结束本机 Agent 进程树。
func (s *localAgentSession) close() {
	_ = s.process.Close()
}

// tailBuffer 保留写入内容的末尾 limit 字节。
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

// Write 追加内容并丢弃超出上限的开头部分。
func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if extra := len(b.data) - b.limit; extra > 0 {
		b.data = b.data[extra:]
	}
	return len(p), nil
}

// String 返回保留的内容。
func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// acpClient 是本机 Agent 的 ACP 客户端：把当前一轮的会话更新转换为过程更新，把权限请求交给裁决，并累积最终回复。
type acpClient struct {
	mu   sync.Mutex
	sink turnSink
	// steps 是当前一轮各步骤的最新状态，按步骤编号索引。
	steps map[string]*domain.ToolCallStep
	// reply 是最后一个步骤之后的回复正文。
	reply strings.Builder
}

var _ acp.Client = (*acpClient)(nil)

// begin 开始新的一轮，过程更新交给 sink。
func (c *acpClient) begin(sink turnSink) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sink, c.steps = sink, map[string]*domain.ToolCallStep{}
	c.reply.Reset()
}

// finish 结束当前一轮并返回最终回复。
func (c *acpClient) finish() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sink = nil
	return strings.TrimSpace(c.reply.String())
}

// SessionUpdate 把回复、思考、步骤与任务清单更新转换为过程更新，步骤的增量更新合并为完整状态后上报；不在一轮中时忽略。
func (c *acpClient) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sink == nil {
		return nil
	}
	update := params.Update
	switch {
	case update.AgentMessageChunk != nil:
		if text := contentText(update.AgentMessageChunk.Content); text != "" {
			c.reply.WriteString(text)
			c.sink.update(domain.ToolCallUpdate{Kind: domain.ToolCallUpdateMessage, Text: text})
		}
	case update.AgentThoughtChunk != nil:
		if text := contentText(update.AgentThoughtChunk.Content); text != "" {
			c.sink.update(domain.ToolCallUpdate{Kind: domain.ToolCallUpdateThought, Text: text})
		}
	case update.ToolCall != nil:
		call := update.ToolCall
		step := &domain.ToolCallStep{ID: string(call.ToolCallId), Title: call.Title, Kind: string(call.Kind), Status: string(call.Status),
			Locations: stepLocations(call.Locations), Content: stepContent(call.Content)}
		c.steps[step.ID] = step
		c.reply.Reset()
		c.sink.update(domain.ToolCallUpdate{Kind: domain.ToolCallUpdateStep, Step: cloneStep(step)})
	case update.ToolCallUpdate != nil:
		call := update.ToolCallUpdate
		step := c.steps[string(call.ToolCallId)]
		if step == nil {
			step = &domain.ToolCallStep{ID: string(call.ToolCallId)}
			c.steps[step.ID] = step
		}
		step.Title = support.DerefOr(call.Title, step.Title)
		if call.Kind != nil {
			step.Kind = string(*call.Kind)
		}
		if call.Status != nil {
			step.Status = string(*call.Status)
		}
		if call.Locations != nil {
			step.Locations = stepLocations(call.Locations)
		}
		if call.Content != nil {
			step.Content = stepContent(call.Content)
		}
		c.reply.Reset()
		c.sink.update(domain.ToolCallUpdate{Kind: domain.ToolCallUpdateStep, Step: cloneStep(step)})
	case update.Plan != nil:
		plan := arr.Map(update.Plan.Entries, func(entry acp.PlanEntry) domain.PlanStep {
			return domain.PlanStep{Content: entry.Content, Status: string(entry.Status)}
		})
		c.sink.update(domain.ToolCallUpdate{Kind: domain.ToolCallUpdatePlan, Plan: plan})
	}
	return nil
}

// RequestPermission 把权限请求交给这一轮的发起人裁决，按裁决选用处理方式；不在一轮中或请求已取消时按取消回复。
func (c *acpClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	sink := c.sink
	c.mu.Unlock()
	cancelled := acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{Outcome: "cancelled"}}}
	if sink == nil {
		return cancelled, nil
	}
	call := params.ToolCall
	request := domain.LocalAgentPermission{Step: domain.ToolCallStep{ID: string(call.ToolCallId), Title: support.Deref(call.Title), Locations: stepLocations(call.Locations), Content: stepContent(call.Content)}}
	if call.Kind != nil {
		request.Step.Kind = string(*call.Kind)
	}
	// 请求中的步骤只给出变化部分时，以当前一轮已知的步骤补全标题与类型。
	c.mu.Lock()
	if known := c.steps[request.Step.ID]; known != nil {
		request.Step.Title = cmp.Or(request.Step.Title, known.Title)
		request.Step.Kind = cmp.Or(request.Step.Kind, known.Kind)
		if len(request.Step.Content) == 0 {
			request.Step.Content = known.Content
		}
	}
	c.mu.Unlock()
	request.Options = arr.Map(params.Options, func(option acp.PermissionOption) domain.LocalAgentPermissionOption {
		return domain.LocalAgentPermissionOption{ID: string(option.OptionId), Name: option.Name, Kind: string(option.Kind)}
	})
	optionID, err := sink.permission(ctx, request)
	if err != nil || optionID == "" {
		return cancelled, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(optionID)}}}, nil
}

// errClientCapability 是本机 Agent 调用未声明的客户端能力时返回的错误。
var errClientCapability = errors.New("这台电脑不提供该能力")

// ReadTextFile 不提供：本机 Agent 使用自身的文件工具。
func (c *acpClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errClientCapability
}

// WriteTextFile 不提供：本机 Agent 使用自身的文件工具。
func (c *acpClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errClientCapability
}

// CreateTerminal 不提供：本机 Agent 使用自身的命令工具。
func (c *acpClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errClientCapability
}

// KillTerminal 不提供。
func (c *acpClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errClientCapability
}

// TerminalOutput 不提供。
func (c *acpClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errClientCapability
}

// ReleaseTerminal 不提供。
func (c *acpClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errClientCapability
}

// WaitForTerminalExit 不提供。
func (c *acpClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errClientCapability
}

// contentText 返回内容块中的文本，非文本内容返回空。
func contentText(block acp.ContentBlock) string {
	if block.Text == nil {
		return ""
	}
	return block.Text.Text
}

// stepLocations 返回步骤涉及的文件路径。
func stepLocations(locations []acp.ToolCallLocation) []string {
	return arr.Map(locations, func(location acp.ToolCallLocation) string { return location.Path })
}

// stepContent 转换步骤产出的文本与文件差异，其他内容不记录。
func stepContent(content []acp.ToolCallContent) []domain.ToolCallStepContent {
	result := make([]domain.ToolCallStepContent, 0, len(content))
	for _, item := range content {
		switch {
		case item.Diff != nil:
			result = append(result, domain.ToolCallStepContent{Diff: &domain.FileDiff{Path: item.Diff.Path, OldText: item.Diff.OldText, NewText: item.Diff.NewText}})
		case item.Content != nil:
			if text := contentText(item.Content.Content); text != "" {
				result = append(result, domain.ToolCallStepContent{Text: text})
			}
		}
	}
	return result
}

// cloneStep 复制步骤，上报的状态与之后的合并互不影响。
func cloneStep(step *domain.ToolCallStep) *domain.ToolCallStep {
	copied := *step
	copied.Locations = slices.Clone(step.Locations)
	copied.Content = slices.Clone(step.Content)
	return &copied
}
