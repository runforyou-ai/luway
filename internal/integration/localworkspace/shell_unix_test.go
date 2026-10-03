//go:build !windows

package localworkspace

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRun 验证命令在默认文件夹中执行，合并输出并注明非零退出码。
func TestRun(t *testing.T) {
	root := t.TempDir()
	workspace := New(root, Environment{})
	output, err := workspace.Run(context.Background(), "pwd; echo err >&2; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(root)
	inRoot := strings.Contains(output, root) || strings.Contains(output, resolved)
	if !inRoot || !strings.Contains(output, "err") || !strings.Contains(output, "[命令以退出码 3 结束]") {
		t.Fatalf("output=%q", output)
	}
	if output, err := workspace.Run(context.Background(), "true"); err != nil || output != "[命令执行成功，没有输出]" {
		t.Fatalf("empty output=%q err=%v", output, err)
	}
}

// TestRunTerminatesProcessGroup 验证超时、取消与命令结束都终止命令启动的整个进程组。
func TestRunTerminatesProcessGroup(t *testing.T) {
	root := t.TempDir()
	workspace := New(root, Environment{})
	marker := filepath.Join(root, "survivor")
	// 后台子进程在 1 秒后写入标记文件，进程组被终止时不会写入。
	background := "(sleep 1; touch " + marker + ") > /dev/null 2>&1 &"
	workspace.timeout = 200 * time.Millisecond
	started := time.Now()
	output, err := workspace.Run(context.Background(), background+" sleep 30")
	if err != nil || !strings.Contains(output, "[命令超时，已终止]") || time.Since(started) > 5*time.Second {
		t.Fatalf("timeout output=%q err=%v elapsed=%v", output, err, time.Since(started))
	}
	workspace.timeout = CommandTimeout
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := workspace.Run(ctx, background+" sleep 30"); err == nil {
		t.Fatal("cancelled command returned no error")
	}
	if output, err := workspace.Run(context.Background(), background+" echo done"); err != nil || !strings.Contains(output, "done") {
		t.Fatalf("finished output=%q err=%v", output, err)
	}
	// 后台进程继承输出管道时，主进程退出后同样立即终止。
	inherited := "(sleep 1; touch " + marker + ") & echo parent_done"
	if output, err := workspace.Run(context.Background(), inherited); err != nil || !strings.Contains(output, "parent_done") {
		t.Fatalf("inherited output=%q err=%v", output, err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("background process survived: %v", err)
	}
}

// TestReadNamedPipe 验证命名管道不会被读取，调用立即返回。
func TestReadNamedPipe(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := New(root, Environment{}).ReadText("pipe", 0, 0); err == nil {
			t.Error("read named pipe succeeded")
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("reading named pipe blocked")
	}
}

// TestReadLoginEnvironment 验证读取登录环境时忽略 profile 输出、立即终止 profile 启动的后台进程，并去掉启动文件变量。
func TestReadLoginEnvironment(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "survivor")
	shell := filepath.Join(dir, "fake-shell")
	script := "#!/bin/sh\necho profile-noise\n(sleep 1; touch " + marker + ") &\nexport BASH_ENV=/tmp/startup ENV=/tmp/startup APP_LOGIN=yes\nexec /bin/sh -c \"$3\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	environment, err := readLoginEnvironment(context.Background(), shell)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if !strings.Contains(joined, "APP_LOGIN=yes") || strings.Contains(joined, "profile-noise") || strings.Contains(joined, "BASH_ENV=") || strings.Contains(joined, "\nENV=") {
		t.Fatalf("environment=%v", environment)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("profile background process survived: %v", err)
	}
}

// TestReadLoginEnvironmentCancel 验证读取登录环境响应调用方取消并终止登录 shell。
func TestReadLoginEnvironmentCancel(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "profile-done")
	shell := filepath.Join(dir, "slow-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nsleep 1\ntouch "+marker+"\nexec /bin/sh -c \"$3\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := readLoginEnvironment(ctx, shell); err == nil {
		t.Fatal("cancelled read returned environment")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("login shell survived cancel: %v", err)
	}
}

// TestStartProcessTerminatesProcessTree 验证关闭标准输入时终止命令及其启动的子进程。
func TestStartProcessTerminatesProcessTree(t *testing.T) {
	process, err := StartProcess(context.Background(), Environment{}, t.TempDir(), nil, "sh", "-c", "sleep 30 & echo $!; wait")
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(process.Stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Stdin.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("子进程 %d 未被终止", child)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestStartProcessTerminatesDetachedDescendants 验证终止进程树时一并终止已移入独立进程组的后代进程。
func TestStartProcessTerminatesDetachedDescendants(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl is unavailable")
	}
	process, err := StartProcess(context.Background(), Environment{}, t.TempDir(), nil, "sh", "-c",
		`perl -MPOSIX -e 'POSIX::setsid(); print "$$\n"; STDOUT->flush(); sleep 30' & wait`)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(process.Stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	detached, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(detached, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(detached, syscall.SIGKILL)
			t.Fatalf("独立进程组的后代进程 %d 未被终止", detached)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
