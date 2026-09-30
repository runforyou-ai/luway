//go:build server

package filecontent

import (
	"context"
	"strings"
	"testing"
)

// TestLocalStoreSave 验证本地存储按受控键原子写入内容。
func TestLocalStoreSave(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "organizations/org/files/file.png"
	if err := store.Save(context.Background(), key, strings.NewReader("avatar"), 6); err != nil {
		t.Fatal(err)
	}
	file, info, err := store.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if info.Size() != 6 {
		t.Fatalf("size = %d, want 6", info.Size())
	}
	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatalf("delete missing file: %v", err)
	}
	if _, err := store.Stat(context.Background(), "../outside"); err == nil {
		t.Fatal("expected traversal key to fail")
	}
}

// TestLocalStoreRejectsUnexpectedSize 验证本地上传内容大小必须匹配元数据。
func TestLocalStoreRejectsUnexpectedSize(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), "file", strings.NewReader("too long"), 3); err == nil {
		t.Fatal("expected size mismatch")
	}
	if _, err := store.Stat(context.Background(), "file"); err == nil {
		t.Fatal("unexpected committed file")
	}
}
