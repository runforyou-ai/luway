//go:build !server && !ios && !android

package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/localmcp"
	"github.com/runforyou-ai/luway/internal/integration/localskill"
	"github.com/runforyou-ai/luway/internal/integration/localworkspace"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
)

// newTestHost 创建以临时目录为会话文件夹根目录、带一个技能的本机执行环境。
func newTestHost(t *testing.T) (*Host, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "folders")
	skillsDir := filepath.Join(t.TempDir(), "skills")
	skill := filepath.Join(skillsDir, "xlsx")
	if err := os.MkdirAll(filepath.Join(skill, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, localskill.FileName), []byte("---\nname: xlsx\ndescription: 处理 Excel 表格\n---\n用 scripts/recalc.py 重算"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "scripts", "recalc.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	host := NewHost(HostOptions{
		FolderRoot: root,
		Toolchain:  func() (localworkspace.Environment, bool) { return localworkspace.Environment{}, false },
		MCP:        localmcp.NewStore(filepath.Join(t.TempDir(), "mcp.json"), func() {}),
		Skills:     localskill.NewStore([]localskill.Dir{{Path: skillsDir, Source: localskill.SourceManaged}}, func() {}),
	})
	t.Cleanup(host.Close)
	return host, root
}

// TestHostFileOperations 验证文件操作在会话默认文件夹中执行，写入与修改按读取时的摘要校验，非单级文件夹名称按会话文件夹根目录处理。
func TestHostFileOperations(t *testing.T) {
	ctx := context.Background()
	host, root := newTestHost(t)
	created := host.Execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationWriteFile, Folder: "conv-1", Path: "notes.txt", Content: "a\nb"})
	if created.Error != "" || created.Path != filepath.Join(root, "conv-1", "notes.txt") || created.Hash == "" {
		t.Fatalf("created=%+v", created)
	}
	read := host.Execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationReadFile, Folder: "conv-1", Path: "notes.txt"})
	if read.Error != "" || read.Output != "     1\ta\n     2\tb" || read.Hash != created.Hash {
		t.Fatalf("read=%+v", read)
	}
	if stale := host.Execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationEditFile, Folder: "conv-1", Path: "notes.txt", OldString: "a", NewString: "c", BaseHash: "stale"}); !strings.Contains(stale.Error, "已被改动") {
		t.Fatalf("stale edit=%+v", stale)
	}
	edited := host.Execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationEditFile, Folder: "conv-1", Path: "notes.txt", OldString: "a", NewString: "c", BaseHash: read.Hash})
	if edited.Error != "" || edited.Hash == read.Hash {
		t.Fatalf("edited=%+v", edited)
	}
	escaped := host.Execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationWriteFile, Folder: "../outside", Path: "x.txt", Content: "x"})
	if escaped.Error != "" || escaped.Path != filepath.Join(root, "x.txt") {
		t.Fatalf("escaped=%+v", escaped)
	}
	if unknown := host.Execute(ctx, domain.ComputerOperation{Kind: "browser"}); !strings.Contains(unknown.Error, "请更新应用") {
		t.Fatalf("unknown=%+v", unknown)
	}
}

// TestHostCommandAndSkill 验证命令在会话默认文件夹中执行，技能读取返回说明正文、所在文件夹与附带文件。
func TestHostCommandAndSkill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("命令语法按 Unix shell 编写")
	}
	ctx := context.Background()
	host, root := newTestHost(t)
	output := host.Execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationCommand, Folder: "conv-2", Command: "pwd"})
	resolved, _ := filepath.EvalSymlinks(filepath.Join(root, "conv-2"))
	if output.Error != "" || !strings.Contains(output.Output, "conv-2") || (!strings.Contains(output.Output, filepath.Join(root, "conv-2")) && !strings.Contains(output.Output, resolved)) {
		t.Fatalf("command=%+v", output)
	}
	skill := host.Execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationLoadSkill, Skill: "xlsx"})
	if skill.Error != "" || !strings.Contains(skill.Output, "重算") || len(skill.Files) != 1 || skill.Files[0] != "scripts/recalc.py" {
		t.Fatalf("skill=%+v", skill)
	}
	capabilities, err := host.Capabilities(ctx)
	if err != nil || capabilities.Shell != "bash" || capabilities.FolderRoot != root || len(capabilities.Skills) != 1 || len(capabilities.MCPServers) != 0 {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
}

// fakeServer 模拟服务端的执行器接口：事件流发送一次连接确认，之后每次 work 有信号时发送待执行操作通知；领取接口返回一次预设操作，
// 执行器持有 abort 中的操作时要求中止；记录上报的结果与执行能力。
type fakeServer struct {
	mu           sync.Mutex
	credential   string
	pending      []appservice.ComputerOperationItem
	abort        []string
	work         chan struct{}
	outcomes     map[string]domain.ComputerOutcome
	capabilities []appservice.ComputerCapabilitiesInput
}

// ServeHTTP 按路径处理执行器请求，凭据不符时返回 401。
func (s *fakeServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+s.credential {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case request.URL.Path == "/api/realtime/computer":
		writer.Header().Set("Content-Type", "text/event-stream")
		data, _ := protocol.Encode(protocol.ServerHello{ConnectionID: "c1"})
		_, _ = writer.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		writer.(http.Flusher).Flush()
		s.mu.Unlock()
		for {
			select {
			case <-request.Context().Done():
				s.mu.Lock()
				return
			case <-s.work:
				data, _ := protocol.Encode(protocol.ComputerWork{})
				_, _ = writer.Write(append(append([]byte("data: "), data...), '\n', '\n'))
				writer.(http.Flusher).Flush()
			}
		}
	case request.URL.Path == "/api/computer/capabilities":
		var input appservice.ComputerCapabilitiesInput
		_ = json.NewDecoder(request.Body).Decode(&input)
		s.capabilities = append(s.capabilities, input)
		writer.WriteHeader(http.StatusNoContent)
	case request.URL.Path == "/api/computer/operations/claim":
		var input appservice.ComputerClaimInput
		_ = json.NewDecoder(request.Body).Decode(&input)
		abort := make([]string, 0)
		for _, id := range input.Running {
			if slices.Contains(s.abort, id) {
				abort = append(abort, id)
			}
		}
		_ = json.NewEncoder(writer).Encode(appservice.ComputerOperationList{Operations: s.pending, Abort: abort})
		s.pending = nil
	case strings.HasPrefix(request.URL.Path, "/api/computer/operations/"):
		id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/api/computer/operations/"), "/result")
		var input appservice.ComputerOutcomeInput
		_ = json.NewDecoder(request.Body).Decode(&input)
		s.outcomes[id] = input.Outcome
		writer.WriteHeader(http.StatusNoContent)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

// TestLinkClaimsExecutesAndReports 验证连接建立后上报执行能力，收到连接确认即领取操作，执行后上报结果；凭据失效时停止连接并通知。
func TestLinkClaimsExecutesAndReports(t *testing.T) {
	host, root := newTestHost(t)
	server := &fakeServer{credential: "secret", outcomes: map[string]domain.ComputerOutcome{}, pending: []appservice.ComputerOperationItem{
		{ID: "op-1", Operation: domain.ComputerOperation{Kind: domain.ComputerOperationWriteFile, Folder: "conv", Path: "a.txt", Content: "hi"}},
	}}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	link, err := StartLink(LinkOptions{ServerURL: httpServer.URL, Credential: "secret", Host: host})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		server.mu.Lock()
		outcome, done := server.outcomes["op-1"]
		reported := len(server.capabilities)
		server.mu.Unlock()
		if done && reported > 0 {
			if outcome.Error != "" || outcome.Path != filepath.Join(root, "conv", "a.txt") {
				t.Fatalf("outcome=%+v", outcome)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("outcome reported=%v capabilities=%d", done, reported)
		}
		time.Sleep(20 * time.Millisecond)
	}
	link.Stop()
	server.mu.Lock()
	if server.capabilities[0].ExecutorVersion != Version || server.capabilities[0].MaxConcurrency != defaultConcurrency {
		t.Fatalf("capabilities=%+v", server.capabilities[0])
	}
	server.mu.Unlock()

	invalid := make(chan struct{})
	revoked, err := StartLink(LinkOptions{ServerURL: httpServer.URL, Credential: "old", Host: host, OnCredentialInvalid: func() { close(invalid) }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-invalid:
	case <-time.After(5 * time.Second):
		t.Fatal("credential invalid not reported")
	}
	revoked.Stop()
}

// TestLinkAbortsOperation 验证执行器领取时收到中止要求后结束正在执行的命令，并把结果上报为已中止。
func TestLinkAbortsOperation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("命令语法按 Unix shell 编写")
	}
	host, _ := newTestHost(t)
	server := &fakeServer{credential: "secret", outcomes: map[string]domain.ComputerOutcome{}, abort: []string{"op-slow"},
		work: make(chan struct{}, 1), pending: []appservice.ComputerOperationItem{
			{ID: "op-slow", Operation: domain.ComputerOperation{Kind: domain.ComputerOperationCommand, Folder: "conv", Command: "sleep 30"}},
		}}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	link, err := StartLink(LinkOptions{ServerURL: httpServer.URL, Credential: "secret", Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer link.Stop()
	started := time.Now()
	deadline := started.Add(10 * time.Second)
	for {
		// 命令开始后发送待执行操作通知，执行器随即领取并收到中止要求。
		select {
		case server.work <- struct{}{}:
		default:
		}
		server.mu.Lock()
		outcome, done := server.outcomes["op-slow"]
		server.mu.Unlock()
		if done {
			if !outcome.Aborted {
				t.Fatalf("outcome=%+v", outcome)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("aborted outcome not reported")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("abort took %v", elapsed)
	}
}
