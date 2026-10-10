//go:build server

package integrationtest

import (
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/jetcast"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// executorProcess 是本轮集成测试独占的真实无界面执行器进程。
type executorProcess struct {
	command *exec.Cmd
	done    chan error
	log     string
}

// launchExecutor 以独立数据目录启动执行器并在测试结束时等待进程退出。
func launchExecutor(t *testing.T, binary, address, credential string) *executorProcess {
	t.Helper()
	data := t.TempDir()
	logPath := filepath.Join(data, "process.log")
	output, err := os.Create(logPath)
	require.NoError(t, err)
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "EXECUTOR_SERVER_URL="+address, "EXECUTOR_CREDENTIAL="+credential, "EXECUTOR_DATA_DIR="+data)
	command.Stdout, command.Stderr = output, output
	require.NoError(t, command.Start())
	process := &executorProcess{command: command, done: make(chan error, 1), log: logPath}
	go func() { process.done <- command.Wait(); close(process.done) }()
	t.Cleanup(func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-process.done:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-process.done
		}
		_ = output.Close()
		if t.Failed() {
			content, _ := os.ReadFile(logPath)
			t.Log(string(content))
		}
	})
	return process
}

// queueExecutorOperation 写入真实领取与回执链路使用的待执行调用。
func queueExecutorOperation(t *testing.T, f conversationFileFixture, computerID, runID, command string) string {
	t.Helper()
	call := servermodels.AgentToolCall{ID: uuid.NewV7().String(), WorkspaceID: f.owner.Workspace.ID, AgentRunID: runID,
		ModelCallID: uuid.NewV7().String(), ProviderCallID: uuid.NewV7().String(), Name: "run_command", Source: string(domain.AgentToolSourceBuiltin), Arguments: "{}",
		Replayable: true, Status: string(domain.AgentToolCallQueued), ComputerID: &computerID,
		Operation: &domain.ComputerCall{Operation: domain.ComputerOperation{Kind: domain.ComputerOperationCommand, Folder: f.groupID, Command: command}}}
	_, err := f.db.NewInsert().Model(&call).Exec(t.Context())
	require.NoError(t, err)
	return call.ID
}

// awaitExecutorOperation 等待真实执行器完成操作并核验服务端持久回执。
func awaitExecutorOperation(t *testing.T, f conversationFileFixture, id string, timeout time.Duration) {
	t.Helper()
	var call servermodels.AgentToolCall
	require.Eventually(t, func() bool {
		err := f.db.NewSelect().Model(&call).Where("atc.id = ?", id).Scan(t.Context())
		return err == nil && domain.AgentToolCallStatus(call.Status).Settled()
	}, timeout, 50*time.Millisecond)
	require.Equal(t, string(domain.AgentToolCallSucceeded), call.Status, "result=%v error=%v", call.Result, call.Error)
	require.NotNil(t, call.Operation.Outcome)
	require.Empty(t, call.Operation.Outcome.Error)
	require.NotContains(t, call.Operation.Outcome.Output, "未能同步")
}

// TestJetcastExecutorLifecycle 验证真实执行器的工作通知、文件上传下载、HTTP 心跳、断线轮询和凭据轮换退出。
func TestJetcastExecutorLifecycle(t *testing.T) {
	f := newConversationFileFixture(t)
	h := startRealtimeGateway(t, f.navigationFixture)
	ctx := t.Context()
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	binaries := t.TempDir()
	build := exec.CommandContext(ctx, "wails3", "task", "build:executor", "BIN_DIR="+binaries)
	build.Dir = root
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	binary := filepath.Join(binaries, brand.Build().Slug+"-executor-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, f.local, nil, agentrunaction.NewRunCancellation(f.db, testEnqueuer), testEnqueuer, nil, nil, nil)
	proxy, err := h.broker.Proxy()
	require.NoError(t, err)
	var disconnected atomic.Bool
	var claims atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/nats", func(w http.ResponseWriter, r *http.Request) {
		if disconnected.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	service := api.NewService(backend, api.WithComputers(backend))
	mux.Handle("/api/", http.StripPrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/computer/operations/claim" {
			claims.Add(1)
		}
		service.ServeHTTP(w, r)
	})))
	mux.Handle(domain.LocalFilePublicPath+"/", http.StripPrefix(domain.LocalFilePublicPath+"/", api.NewLocalObjectService(direct.NewLocalObjectAuthorizer(f.db), f.local)))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	registered, err := computeraction.NewCreateWorkspaceComputerAction(f.db).Execute(ctx, f.owner, "集成执行器")
	require.NoError(t, err)
	meta := appservice.RequestMeta{Token: registered.Credential, ExecutorVersion: domain.ExecutorVersion}
	config, err := backend.GetComputerRealtimeConnection(ctx, meta)
	require.NoError(t, err)
	uncooperative := rawPeer(t, server.URL, config)
	var seen *time.Time
	require.NoError(t, f.db.NewSelect().Model((*servermodels.Computer)(nil)).Column("last_seen_at").Where("id = ?", registered.Record.ID).Scan(ctx, &seen))
	require.Nil(t, seen, "NATS 连接不记录电脑在线")
	runID := insertAgentRun(t, f.db, f.owner.Workspace.ID, f.groupID)
	revision := servermodels.AgentRevision{ID: uuid.NewV7().String(), WorkspaceID: f.owner.Workspace.ID, AgentID: uuid.NewV7().String(), ExecutionMode: string(domain.AgentExecutionModeManaged), SchemaVersion: 1, Configuration: []byte("{}"), CreatedByUserID: f.member.User.ID, CreatedAt: time.Now()}
	_, err = f.db.NewInsert().Model(&revision).Exec(ctx)
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Model((*servermodels.AgentRun)(nil)).Set("agent_identity_id = ?", f.member.WorkspaceIdentity.ID).Set("agent_revision_id = ?", revision.ID).Where("id = ?", runID).Exec(ctx)
	require.NoError(t, err)
	_, err = f.members.AddUpload(ctx, f.member, f.groupID, f.upload(t, f.member, domain.FilePurposeConversationFile, "input.txt", []byte("file-stream-regression")))
	require.NoError(t, err)
	process := launchExecutor(t, binary, server.URL, registered.Credential)
	require.Eventually(t, func() bool {
		var online bool
		err := f.db.NewSelect().Model((*servermodels.Computer)(nil)).ColumnExpr(servermodels.ComputerOnlineExpr("cmp")).Where("id = ?", registered.Record.ID).Scan(ctx, &online)
		return err == nil && online && claims.Load() > 0
	}, 15*time.Second, 25*time.Millisecond)
	first := queueExecutorOperation(t, f, registered.Record.ID, runID, "cat shared/input.txt > shared/output.txt")
	require.NoError(t, h.members.Publish(ctx, realtime.ComputerWork(f.owner.Workspace.ID, registered.Record.ID)))
	awaitExecutorOperation(t, f, first, 10*time.Second)
	entries, err := f.members.List(ctx, f.member, f.groupID)
	require.NoError(t, err)
	found := false
	for _, entry := range entries {
		if entry.Path == "output.txt" {
			found = true
			require.Equal(t, []byte("file-stream-regression"), f.content(t, f.member, entry.ID))
		}
	}
	require.True(t, found, "执行器通过保留的 HTTP 文件流写回共享文件")
	// 工作通知连接关闭且代理持续不可用时，业务领取轮询仍完成末尾未通知的任务。
	disconnected.Store(true)
	_, err = h.members.Server.Disconnect(ctx, jetcast.ByUser(config.UserID))
	require.NoError(t, err)
	require.Eventually(t, uncooperative.IsClosed, 5*time.Second, 10*time.Millisecond)
	count := claims.Load()
	require.Eventually(t, func() bool { return claims.Load() > count }, 35*time.Second, 50*time.Millisecond)
	second := queueExecutorOperation(t, f, registered.Record.ID, runID, "cat shared/input.txt > shared/polled.txt")
	started := time.Now()
	awaitExecutorOperation(t, f, second, 40*time.Second)
	require.Greater(t, time.Since(started), 20*time.Second, "通过 30 秒业务轮询领取")
	disconnected.Store(false)
	validRegistration, err := computeraction.NewCreateWorkspaceComputerAction(f.db).Execute(ctx, f.owner, "保留的电脑")
	require.NoError(t, err)
	validConfig, err := backend.GetComputerRealtimeConnection(ctx, appservice.RequestMeta{Token: validRegistration.Credential, ExecutorVersion: domain.ExecutorVersion})
	require.NoError(t, err)
	valid := connectPeer(t, f.db, server.URL, validConfig)
	rotated, err := computeraction.NewResetComputerCredentialAction(f.db, newTestToolDecisions(f.db, testEnqueuer)).Execute(ctx, f.owner, registered.Record.ID)
	require.NoError(t, err)
	require.Error(t, backend.HeartbeatComputer(ctx, meta))
	require.ErrorIs(t, computeraction.NewTouchComputerAction(f.db).Execute(ctx, computeraction.Identity{WorkspaceID: f.owner.Workspace.ID, ComputerID: registered.Record.ID}, registered.Credential), computeraction.ErrCredentialInvalid)
	select {
	case err := <-process.done:
		require.Error(t, err)
	case <-time.After(35 * time.Second):
		t.Fatal("凭据轮换后执行器未退出")
	}
	require.NoError(t, h.members.Publish(ctx, realtime.ComputerWork(f.owner.Workspace.ID, validRegistration.Record.ID)))
	valid.expect(protocol.ComputerWork{})
	restarted := launchExecutor(t, binary, server.URL, rotated.Credential)
	third := queueExecutorOperation(t, f, registered.Record.ID, runID, "cat shared/input.txt > shared/restarted.txt")
	require.NoError(t, h.members.Publish(ctx, realtime.ComputerWork(f.owner.Workspace.ID, registered.Record.ID)))
	awaitExecutorOperation(t, f, third, 15*time.Second)
	require.NoError(t, restarted.command.Process.Signal(os.Interrupt))
	select {
	case err := <-restarted.done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("停止执行器未释放连接")
	}
	// 初次连接尚未就绪时退出也必须取消 SDK 的连接重试。
	disconnected.Store(true)
	pending := launchExecutor(t, binary, server.URL, rotated.Credential)
	require.Eventually(t, func() bool { data, _ := os.ReadFile(pending.log); return len(data) > 0 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, pending.command.Process.Signal(os.Interrupt))
	select {
	case err := <-pending.done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("首次连接取消后仍在重试")
	}
}
