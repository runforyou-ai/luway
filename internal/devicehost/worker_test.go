//go:build !server && !ios && !android

package devicehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/luway/internal/integration/knowledgeretrieval"
	"github.com/runforyou-ai/luway/internal/integration/localmcp"
	"github.com/runforyou-ai/luway/internal/integration/localskill"
	"github.com/runforyou-ai/luway/internal/integration/localworkspace"
	"github.com/runforyou-ai/luway/internal/integration/mcp"
)

// stubRunClient 在内存中模拟设备运行期接口，记录领取、收尾与失败上报。
type stubRunClient struct {
	mu     sync.Mutex
	work   appservice.DeviceWork
	claims []string
	// localAgents 按上报顺序记录每次上报的本机 Agent，reportMetas 记录对应的请求信息。
	localAgents [][]appservice.LocalAgentKind
	reportMetas []appservice.RequestMeta
	// workMetas 记录读取待领取运行的请求信息。
	workMetas []appservice.RequestMeta
	// streams 非空时按工作区保存打开着的事件流。
	streams   map[string]*io.PipeWriter
	completed map[string]string
	failures  map[string]appservice.DeviceRunFailureCode
	// failedBlocks 按运行编号记录失败上报携带的过程内容块。
	failedBlocks map[string]json.RawMessage
	// blockPeek 为 true 时读取输入阻塞到运行 context 结束。
	blockPeek bool
	leaseEnd  bool
	peeked    chan string
	// assignment 非空时作为领取返回的有效配置。
	assignment json.RawMessage
	// searches 记录知识检索请求。
	searches []json.RawMessage
	// mcpLists 记录列出企业 MCP 服务的次数，mcpCalls 记录企业 MCP 工具调用。
	mcpLists int
	mcpCalls []appservice.DeviceRunMCPToolCallInput
}

// GetDeviceWork 返回预设的待领取运行。
func (c *stubRunClient) GetDeviceWork(_ context.Context, meta appservice.RequestMeta) (appservice.DeviceWork, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workMetas = append(c.workMetas, meta)
	return c.work, nil
}

// ReportDeviceLocalAgents 记录上报的本机 Agent。
func (c *stubRunClient) ReportDeviceLocalAgents(_ context.Context, meta appservice.RequestMeta, input appservice.DeviceLocalAgentsInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.localAgents = append(c.localAgents, input.LocalAgents)
	c.reportMetas = append(c.reportMetas, meta)
	return nil
}

// ClaimDeviceRun 记录领取并返回预设的有效配置。
func (c *stubRunClient) ClaimDeviceRun(_ context.Context, _ appservice.RequestMeta, runID string) (appservice.DeviceRunClaim, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.claims = append(c.claims, runID)
	assignment := c.assignment
	if assignment == nil {
		assignment = json.RawMessage("{}")
	}
	return appservice.DeviceRunClaim{Assignment: assignment, LeaseExpiresAt: time.Now().Add(time.Minute), LeaseRenewIntervalSeconds: 3600}, nil
}

// RenewDeviceRunLease 按预设返回运行是否已结束。
func (c *stubRunClient) RenewDeviceRunLease(context.Context, appservice.RequestMeta, string) (appservice.DeviceRunLease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return appservice.DeviceRunLease{Ended: c.leaseEnd}, nil
}

// PeekDeviceRunInputs 返回一条输入，需要时阻塞到运行被取消。
func (c *stubRunClient) PeekDeviceRunInputs(ctx context.Context, _ appservice.RequestMeta, runID string, _ appservice.DeviceRunInputPeekInput) (appservice.DeviceRunInputSignals, error) {
	c.mu.Lock()
	block := c.blockPeek
	c.mu.Unlock()
	if c.peeked != nil {
		c.peeked <- runID
	}
	if block {
		<-ctx.Done()
		return appservice.DeviceRunInputSignals{}, ctx.Err()
	}
	return appservice.DeviceRunInputSignals{Seqs: []int64{1}}, nil
}

// ClaimDeviceRunInputs 返回两条上下文消息。
func (c *stubRunClient) ClaimDeviceRunInputs(context.Context, appservice.RequestMeta, string, appservice.DeviceRunInputClaimInput) (appservice.DeviceRunClaimedInput, error) {
	return appservice.DeviceRunClaimedInput{EndSeq: 1, Messages: json.RawMessage(`[{},{}]`)}, nil
}

// SearchDeviceRunKnowledge 记录检索请求并返回一条固定记录。
func (c *stubRunClient) SearchDeviceRunKnowledge(_ context.Context, _ appservice.RequestMeta, _ string, input appservice.DeviceRunKnowledgeSearchInput) (appservice.DeviceRunKnowledgeSearchResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.searches = append(c.searches, input.Request)
	return appservice.DeviceRunKnowledgeSearchResult{Result: json.RawMessage(`{"records":[{"content":"退款三天到账"}]}`)}, nil
}

// GetDeviceRunMemory 返回空的助理记忆。
func (c *stubRunClient) GetDeviceRunMemory(context.Context, appservice.RequestMeta, string) (appservice.DeviceRunMemory, error) {
	return appservice.DeviceRunMemory{Entries: json.RawMessage(`[]`)}, nil
}

// SearchDeviceRunWeb 返回一条固定的搜索结果。
func (c *stubRunClient) SearchDeviceRunWeb(_ context.Context, _ appservice.RequestMeta, _ string, _ appservice.DeviceRunWebSearchInput) (appservice.DeviceRunWebSearchResult, error) {
	return appservice.DeviceRunWebSearchResult{Result: json.RawMessage(`{"items":[{"title":"退款政策","url":"https://example.com/refund"}]}`)}, nil
}

// ListDeviceRunMCPTools 记录列出次数并返回一个提供订单查询的企业服务。
func (c *stubRunClient) ListDeviceRunMCPTools(context.Context, appservice.RequestMeta, string) (appservice.DeviceRunMCPToolList, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mcpLists++
	return appservice.DeviceRunMCPToolList{Servers: []appservice.DeviceRunMCPServer{{
		ID: "server-1", Name: "订单系统",
		Tools: []appservice.DeviceRunMCPTool{{Name: "get_order", Description: "查询订单", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}}}, nil
}

// CallDeviceRunMCPTool 记录调用，工具名为 fail 时返回工具报告的失败。
func (c *stubRunClient) CallDeviceRunMCPTool(_ context.Context, _ appservice.RequestMeta, _ string, input appservice.DeviceRunMCPToolCallInput) (appservice.DeviceRunMCPToolCallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mcpCalls = append(c.mcpCalls, input)
	if input.ToolName == "fail" {
		return appservice.DeviceRunMCPToolCallResult{Error: "订单不存在"}, nil
	}
	return appservice.DeviceRunMCPToolCallResult{Result: "订单已发货"}, nil
}

// ReadDeviceRunAttachment 返回附件消息编号对应的固定内容。
func (c *stubRunClient) ReadDeviceRunAttachment(_ context.Context, _ appservice.RequestMeta, _, messageID string) ([]byte, error) {
	return []byte("content:" + messageID), nil
}

// CompleteDeviceRun 记录收尾正文。
func (c *stubRunClient) CompleteDeviceRun(_ context.Context, _ appservice.RequestMeta, runID string, input appservice.DeviceRunResultInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.completed[runID] = input.Content
	return nil
}

// FailDeviceRun 记录失败原因。
func (c *stubRunClient) FailDeviceRun(_ context.Context, _ appservice.RequestMeta, runID string, input appservice.DeviceRunFailureInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures[runID] = input.ErrorCode
	c.failedBlocks[runID] = input.Blocks
	return nil
}

// OpenDeviceEventStream 未设置 streams 时不建立事件流；设置后返回一直保持打开的事件流，并记录各工作区事件流的打开与关闭。
func (c *stubRunClient) OpenDeviceEventStream(_ context.Context, meta appservice.RequestMeta) (io.ReadCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams == nil {
		return nil, io.EOF
	}
	reader, writer := io.Pipe()
	c.streams[meta.WorkspaceID] = writer
	return &trackedStream{PipeReader: reader, onClose: func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.streams[meta.WorkspaceID] == writer {
			delete(c.streams, meta.WorkspaceID)
		}
	}}, nil
}

// openStreams 返回当前打开着事件流的工作区。
func (c *stubRunClient) openStreams() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(maps.Keys(c.streams))
}

// trackedStream 在关闭时通知测试。
type trackedStream struct {
	*io.PipeReader
	onClose func()
}

// Close 关闭事件流并通知测试。
func (s *trackedStream) Close() error {
	s.onClose()
	return s.PipeReader.Close()
}

// DeviceModelEndpoint 返回固定的模型代理入口。
func (c *stubRunClient) DeviceModelEndpoint(context.Context, appservice.RequestMeta, string) (string, http.RoundTripper, error) {
	return "https://app.example.com/api/agent-runs/run/model", http.DefaultTransport, nil
}

// stubRuntime 读取并认领全部输入，以收到的上下文消息数量作为回复；failure 非空时返回该错误与一个过程内容块，inspect 非空时先检查运行请求。
type stubRuntime struct {
	failure error
	inspect func(context.Context, agentruntime.RunRequest)
}

// Run 按预设认领输入并返回回复或失败。
func (r stubRuntime) Run(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
	if r.inspect != nil {
		r.inspect(ctx, request)
	}
	triggers, err := feed.Peek(ctx, 0)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	if r.failure != nil {
		return agentruntime.RunResult{Blocks: []agentruntime.Block{{ID: "block-1"}}}, r.failure
	}
	return agentruntime.RunResult{Content: fmt.Sprintf("收到 %d 条上下文消息", len(claimed.Messages)), EndSeq: claimed.EndSeq}, nil
}

// stubToolchain 按预设返回是否可以领取运行并记录检查次数，不改动命令环境变量；执行循环并行检查各工作区，读写需加锁。
type stubToolchain struct {
	mu     sync.Mutex
	ready  bool
	checks int
}

// Ensure 记录检查并返回预设结果。
func (s *stubToolchain) Ensure() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks++
	return s.ready
}

// setReady 设置是否可以领取运行。
func (s *stubToolchain) setReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
}

// checkCount 返回检查次数。
func (s *stubToolchain) checkCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checks
}

// Environment 返回不改动命令环境变量的设置。
func (s *stubToolchain) Environment() localworkspace.Environment {
	return localworkspace.Environment{}
}

// Close 不做任何事。
func (s *stubToolchain) Close() {}

// newTestWorker 创建已登录并已在工作区 org-1 注册设备 device-1 的执行循环，不启动后台循环。
func newTestWorker(t *testing.T, client *stubRunClient, runtime stubRuntime) *Worker {
	t.Helper()
	store := &stubStore{installID: "install-1", registrations: map[string]string{testServerURL + "|account-1|org-1": "device-1"}}
	registrar, sessions := newTestRegistrar(t, store, &stubClient{serverURL: testServerURL, workspaces: []string{"org-1"}})
	if err := sessions.Establish(context.Background(), credentialFor(testServerURL, "account-1", "token-1")); err != nil {
		t.Fatal(err)
	}
	client.completed = map[string]string{}
	client.failures = map[string]appservice.DeviceRunFailureCode{}
	client.failedBlocks = map[string]json.RawMessage{}
	worker := NewWorker(registrar, client, runtime, &stubToolchain{ready: true}, localmcp.NewStore(filepath.Join(t.TempDir(), "mcp.json"), func() {}),
		localskill.NewStore([]localskill.Dir{{Path: t.TempDir(), Source: localskill.SourceManaged}}, func() {}), t.TempDir(), t.TempDir())
	t.Cleanup(worker.Stop)
	return worker
}

// TestWorkerRunsInConversationFolder 验证各会话的运行都被领取执行并回报回复，本机文件以会话默认文件夹为起点且文件夹自动创建。
func TestWorkerRunsInConversationFolder(t *testing.T) {
	client := &stubRunClient{work: appservice.DeviceWork{Runs: []appservice.DeviceWorkRun{
		{RunID: "run-1", ConversationID: "conversation-1"}, {RunID: "run-2", ConversationID: "conversation-2"},
	}}}
	var mu sync.Mutex
	listed := map[string]bool{}
	worker := newTestWorker(t, client, stubRuntime{inspect: func(ctx context.Context, request agentruntime.RunRequest) {
		_, err := request.Workspace.LsInfo(ctx, &filesystem.LsInfoRequest{})
		mu.Lock()
		listed[request.RunID] = err == nil
		mu.Unlock()
	}})

	worker.poll()
	worker.runs.Wait()
	if len(client.claims) != 2 || client.completed["run-1"] == "" || client.completed["run-2"] == "" || !listed["run-1"] || !listed["run-2"] {
		t.Fatalf("领取 = %v，收尾 = %v，读取默认文件夹 = %v", client.claims, client.completed, listed)
	}
	for _, conversationID := range []string{"conversation-1", "conversation-2"} {
		if info, err := os.Stat(filepath.Join(worker.folders, conversationID)); err != nil || !info.IsDir() {
			t.Fatalf("默认文件夹 %s 未创建：%v", conversationID, err)
		}
	}
}

// TestWorkerWaitsForToolchain 验证运行环境不可领取时不领取运行，可领取后照常领取。
func TestWorkerWaitsForToolchain(t *testing.T) {
	client := &stubRunClient{work: appservice.DeviceWork{
		Runs: []appservice.DeviceWorkRun{{RunID: "run-1", ConversationID: "conversation-1"}},
	}}
	worker := newTestWorker(t, client, stubRuntime{})
	pending := &stubToolchain{}
	worker.toolchain = pending

	worker.poll()
	worker.runs.Wait()
	if len(client.claims) != 0 || pending.checkCount() != 1 {
		t.Fatalf("领取 = %v，检查次数 = %d", client.claims, pending.checkCount())
	}
	pending.setReady(true)
	worker.poll()
	worker.runs.Wait()
	if len(client.claims) != 1 {
		t.Fatalf("运行环境就绪后未领取：%v", client.claims)
	}
}

// TestWorkerStopsEndedRun 验证续租得知运行已结束后中断本机执行，且不上报失败。
func TestWorkerStopsEndedRun(t *testing.T) {
	client := &stubRunClient{
		work:      appservice.DeviceWork{Runs: []appservice.DeviceWorkRun{{RunID: "run-1", ConversationID: "conversation-1"}}},
		blockPeek: true,
		leaseEnd:  true,
		peeked:    make(chan string, 1),
	}
	worker := newTestWorker(t, client, stubRuntime{})

	worker.poll()
	<-client.peeked
	worker.nudgeLeases()
	worker.runs.Wait()
	if len(client.completed) != 0 || len(client.failures) != 0 || len(worker.active) != 0 {
		t.Fatalf("收尾 = %v，失败上报 = %v，本机登记 = %v", client.completed, client.failures, worker.active)
	}
}

// TestWorkerReportsRuntimeFailure 验证运行时出错时上报运行失败并携带已产生的过程内容块。
func TestWorkerReportsRuntimeFailure(t *testing.T) {
	client := &stubRunClient{work: appservice.DeviceWork{Runs: []appservice.DeviceWorkRun{{RunID: "run-1", ConversationID: "conversation-1"}}}}
	worker := newTestWorker(t, client, stubRuntime{failure: errors.New("model unavailable")})

	worker.poll()
	worker.runs.Wait()
	var blocks []agentruntime.Block
	if err := json.Unmarshal(client.failedBlocks["run-1"], &blocks); err != nil {
		t.Fatal(err)
	}
	if client.failures["run-1"] != appservice.DeviceRunFailureRuntimeFailed || len(blocks) != 1 || len(client.completed) != 0 {
		t.Fatalf("失败上报 = %v，过程内容 = %v，收尾 = %v", client.failures, blocks, client.completed)
	}
}

// TestWorkerWiresRunDependencies 验证有效配置含知识检索时经服务端检索、附件经服务端读取，运行流增量可由本机订阅读取且运行结束后结束订阅。
func TestWorkerWiresRunDependencies(t *testing.T) {
	client := &stubRunClient{
		work:       appservice.DeviceWork{Runs: []appservice.DeviceWorkRun{{RunID: "run-1"}}},
		assignment: json.RawMessage(`{"tools":["search_knowledge"]}`),
	}
	var worker *Worker
	var (
		records  []knowledgeretrieval.Record
		content  []byte
		received []runstream.Delta
		ended    bool
		snapshot runstream.Snapshot
	)
	endedOnce := make(chan struct{})
	worker = newTestWorker(t, client, stubRuntime{inspect: func(ctx context.Context, request agentruntime.RunRequest) {
		result, err := request.KnowledgeSearch(ctx, knowledgeretrieval.Request{Queries: []string{"退款"}})
		if err != nil {
			t.Errorf("knowledge search: %v", err)
		}
		records = result.Records
		if content, err = request.ReadAttachment(ctx, "message-1"); err != nil {
			t.Errorf("read attachment: %v", err)
		}
		var subscribed bool
		snapshot, _, subscribed = worker.SubscribeLocalRunStream("run-1", func(delta runstream.Delta) {
			received = append(received, delta)
		}, func() {
			ended = true
			close(endedOnce)
		})
		if !subscribed {
			t.Error("local run stream is not available")
		}
		request.OnStream(runstream.Delta{RunID: "run-1", StreamID: request.StreamID, Attempt: 1, BaseSequence: 0, Sequence: 1,
			Operations: []runstream.Operation{{Kind: runstream.OperationAppendCandidate, Text: "处理中"}}})
	}})

	worker.poll()
	worker.runs.Wait()
	<-endedOnce
	if len(records) != 1 || records[0].Content != "退款三天到账" || len(client.searches) != 1 {
		t.Fatalf("检索结果 = %#v，检索请求 = %s", records, client.searches)
	}
	if string(content) != "content:message-1" {
		t.Fatalf("附件内容 = %q", content)
	}
	if snapshot.RunID != "run-1" || snapshot.StreamID == "" || len(received) != 1 || received[0].Sequence != 1 || !ended {
		t.Fatalf("快照 = %#v，增量 = %#v，结束 = %t", snapshot, received, ended)
	}
	if _, _, ok := worker.SubscribeLocalRunStream("run-1", func(runstream.Delta) {}, func() {}); ok {
		t.Fatal("subscribed to finished local run stream")
	}
}

// TestWorkerProxiesOrganizationMCP 验证有效配置包含企业 MCP 服务时，企业服务排在本地服务之前并经服务端代理读取目录与调用工具。
func TestWorkerProxiesOrganizationMCP(t *testing.T) {
	client := &stubRunClient{
		work:       appservice.DeviceWork{Runs: []appservice.DeviceWorkRun{{RunID: "run-1"}}},
		assignment: json.RawMessage(`{"mcpServers":["订单系统"]}`),
	}
	var (
		servers []agentruntime.MCPServer
		tools   []mcp.Tool
		result  string
		failure error
	)
	worker := newTestWorker(t, client, stubRuntime{inspect: func(ctx context.Context, request agentruntime.RunRequest) {
		servers = request.MCPConnections
		if len(servers) == 0 {
			return
		}
		connection, err := servers[0].Open(ctx)
		if err != nil {
			t.Errorf("open organization MCP: %v", err)
			return
		}
		defer connection.Close()
		if tools, err = connection.Tools(ctx); err != nil {
			t.Errorf("list organization MCP tools: %v", err)
		}
		if result, err = connection.Call(ctx, "get_order", json.RawMessage(`{"id":"A1"}`)); err != nil {
			t.Errorf("call organization MCP tool: %v", err)
		}
		_, failure = connection.Call(ctx, "fail", json.RawMessage(`{}`))
	}})

	worker.poll()
	worker.runs.Wait()
	if len(servers) != 1 || servers[0].Source != agentruntime.MCPSourceOrganization || servers[0].ID != "server-1" || servers[0].Name != "订单系统" {
		t.Fatalf("MCP 连接 = %#v", servers)
	}
	if len(tools) != 1 || tools[0].Name != "get_order" || result != "订单已发货" || failure == nil || failure.Error() != "订单不存在" {
		t.Fatalf("工具目录 = %#v，结果 = %q，失败 = %v", tools, result, failure)
	}
	if client.mcpLists != 1 || len(client.mcpCalls) != 2 || client.mcpCalls[0].ServerID != "server-1" || string(client.mcpCalls[0].Arguments) != `{"id":"A1"}` {
		t.Fatalf("列出次数 = %d，调用 = %#v", client.mcpLists, client.mcpCalls)
	}
}

// TestWorkerOmitsKnowledgeSearch 验证有效配置不含知识检索时不注入检索函数。
func TestWorkerOmitsKnowledgeSearch(t *testing.T) {
	client := &stubRunClient{work: appservice.DeviceWork{Runs: []appservice.DeviceWorkRun{{RunID: "run-1"}}}}
	injected := true
	worker := newTestWorker(t, client, stubRuntime{inspect: func(_ context.Context, request agentruntime.RunRequest) {
		injected = request.KnowledgeSearch != nil
	}})

	worker.poll()
	worker.runs.Wait()
	if injected {
		t.Fatal("knowledge search injected without knowledge tool")
	}
}

// TestWorkerStreamFollowsReservation 验证运行登记后即可订阅本机过程流，释放登记时过程流随之结束。
func TestWorkerStreamFollowsReservation(t *testing.T) {
	worker := newTestWorker(t, &stubRunClient{}, stubRuntime{})
	if !worker.reserve(appservice.DeviceWorkRun{RunID: "run-1"}) || !worker.RunsLocally("run-1") {
		t.Fatal("reserved run is not local")
	}
	ended := false
	snapshot, _, ok := worker.SubscribeLocalRunStream("run-1", func(runstream.Delta) {}, func() { ended = true })
	if !ok || snapshot.RunID != "run-1" || snapshot.StreamID == "" || snapshot.Attempt != 1 {
		t.Fatalf("snapshot = %#v, ok = %v", snapshot, ok)
	}
	worker.release("run-1")
	if !ended || worker.RunsLocally("run-1") {
		t.Fatalf("ended = %v, local = %v", ended, worker.RunsLocally("run-1"))
	}
}

// TestWorkerServesEveryWorkspace 验证执行循环以各工作区的本机设备身份读取待领取运行，并向每个工作区上报本机 Agent，结果未变化时不重复上报。
func TestWorkerServesEveryWorkspace(t *testing.T) {
	client := &stubRunClient{}
	worker := newTestWorker(t, client, stubRuntime{})
	store := worker.registrar.store.(*stubStore)
	store.registrations[testServerURL+"|account-1|org-2"] = "device-2"

	worker.poll()
	want := []appservice.RequestMeta{{DeviceID: "device-1", WorkspaceID: "org-1"}, {DeviceID: "device-2", WorkspaceID: "org-2"}}
	slices.SortFunc(client.workMetas, func(a, b appservice.RequestMeta) int { return strings.Compare(a.WorkspaceID, b.WorkspaceID) })
	if !slices.Equal(client.workMetas, want) {
		t.Fatalf("读取待领取运行的请求 = %#v", client.workMetas)
	}

	sessions, err := worker.registrar.deviceSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kinds := []domain.LocalAgentKind{domain.LocalAgentKindCodex}
	reported := worker.reportLocalAgents(sessions, kinds, nil)
	reported = worker.reportLocalAgents(sessions, kinds, reported)
	if !slices.Equal(client.reportMetas, want) {
		t.Fatalf("上报本机 Agent 的请求 = %#v", client.reportMetas)
	}
	worker.reportLocalAgents(sessions, nil, reported)
	if len(client.reportMetas) != 4 {
		t.Fatalf("本机 Agent 变化后的上报次数 = %d", len(client.reportMetas))
	}
}

// TestWorkerStreamsFollowRegistrations 验证每个已注册工作区各有一条设备事件流，注册结果减少时关闭对应事件流，退出登录后全部关闭。
func TestWorkerStreamsFollowRegistrations(t *testing.T) {
	client := &stubRunClient{streams: map[string]*io.PipeWriter{}}
	worker := newTestWorker(t, client, stubRuntime{})
	store := worker.registrar.store.(*stubStore)
	store.registrations[testServerURL+"|account-1|org-2"] = "device-2"
	worker.loops.Add(1)
	go worker.listen()

	waitForStreams(t, client, []string{"org-1", "org-2"})
	delete(store.registrations, testServerURL+"|account-1|org-2")
	signal(worker.session)
	waitForStreams(t, client, []string{"org-1"})
	if err := worker.registrar.sessions.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	signal(worker.session)
	waitForStreams(t, client, []string{})
}

// waitForStreams 等待打开着事件流的工作区变为预期集合。
func waitForStreams(t *testing.T, client *stubRunClient, want []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		open := client.openStreams()
		if slices.Equal(open, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("打开着事件流的工作区 = %v, want %v", open, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
