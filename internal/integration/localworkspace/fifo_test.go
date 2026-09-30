//go:build !windows

package localworkspace

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

// TestNonRegularFiles 验证命名管道不会被读取或搜索，调用立即返回。
func TestNonRegularFiles(t *testing.T) {
	backend, root, _ := newTestWorkspace(t)
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	requireSymlink(t, filepath.Join(root, "pipe"), filepath.Join(root, "pipe-link"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx := context.Background()
		for _, name := range []string{"pipe", "pipe-link"} {
			if _, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: name}); err == nil {
				t.Errorf("read %s succeeded", name)
			}
			if _, err := backend.MultiModalRead(ctx, &filesystem.MultiModalReadRequest{ReadRequest: filesystem.ReadRequest{FilePath: name}}); err == nil {
				t.Errorf("multimodal read %s succeeded", name)
			}
			if matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "x", Path: name}); err != nil || len(matches) != 0 {
				t.Errorf("grep %s=%+v %v", name, matches, err)
			}
		}
		if _, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "hello"}); err != nil {
			t.Errorf("grep default folder=%v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("reading named pipe blocked")
	}
}
