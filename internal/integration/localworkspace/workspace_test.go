package localworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadText 验证按行读取带行号、按全文计算摘要，偏移超过末尾时给出说明。
func TestReadText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := New(root, Environment{})
	read, err := workspace.ReadText("notes.txt", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if read.Content != "     2\ttwo" || read.Path != filepath.Join(root, "notes.txt") || read.Hash != contentHash([]byte("one\ntwo\nthree\n")) {
		t.Fatalf("read=%+v", read)
	}
	past, err := workspace.ReadText("notes.txt", 10, 0)
	if err != nil || !strings.HasPrefix(past.Content, "没有读到内容") {
		t.Fatalf("past=%+v err=%v", past, err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.ReadText("binary.bin", 0, 0); err == nil {
		t.Fatal("read binary file succeeded")
	}
}

// TestWriteTextRequiresCurrentVersion 验证新建文件不要求摘要，覆盖已有文件必须给出与当前内容一致的摘要。
func TestWriteTextRequiresCurrentVersion(t *testing.T) {
	ctx := context.Background()
	workspace := New(t.TempDir(), Environment{})
	created, err := workspace.WriteText(ctx, "dir/report.md", "v1", "")
	if err != nil || created.Hash != contentHash([]byte("v1")) {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	if _, err := workspace.WriteText(ctx, "dir/report.md", "v2", ""); err == nil || !strings.Contains(err.Error(), "先用 read_file 读取") {
		t.Fatalf("overwrite without version err=%v", err)
	}
	if _, err := workspace.WriteText(ctx, "dir/report.md", "v2", contentHash([]byte("other"))); err == nil || !strings.Contains(err.Error(), "已被改动") {
		t.Fatalf("overwrite stale version err=%v", err)
	}
	updated, err := workspace.WriteText(ctx, "dir/report.md", "v2", created.Hash)
	if err != nil || updated.Hash != contentHash([]byte("v2")) {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
}

// TestEditTextRequiresCurrentVersion 验证修改要求当前内容摘要，原文必须唯一或显式全部替换。
func TestEditTextRequiresCurrentVersion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workspace := New(root, Environment{})
	created, err := workspace.WriteText(ctx, "a.txt", "x x y", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.EditText(ctx, "a.txt", "y", "z", false, ""); err == nil {
		t.Fatal("edit without version succeeded")
	}
	if _, err := workspace.EditText(ctx, "a.txt", "x", "w", false, created.Hash); err == nil || !strings.Contains(err.Error(), "出现 2 次") {
		t.Fatalf("ambiguous edit err=%v", err)
	}
	edited, err := workspace.EditText(ctx, "a.txt", "x", "w", true, created.Hash)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(content) != "w w y" || edited.Hash != contentHash(content) {
		t.Fatalf("content=%q edited=%+v", content, edited)
	}
	if _, err := workspace.EditText(ctx, "a.txt", "y", "z", false, created.Hash); err == nil || !strings.Contains(err.Error(), "已被改动") {
		t.Fatalf("stale edit err=%v", err)
	}
}

// TestWriteTextFollowsSymlink 验证写入符号链接时写入链接目标，链接本身保留。
func TestWriteTextFollowsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	workspace := New(root, Environment{})
	read, err := workspace.ReadText("link.txt", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.WriteText(context.Background(), "link.txt", "new", read.Hash); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(target)
	info, _ := os.Lstat(filepath.Join(root, "link.txt"))
	stat, _ := os.Stat(target)
	if string(content) != "new" || info.Mode()&os.ModeSymlink == 0 || stat.Mode().Perm() != 0o600 {
		t.Fatalf("content=%q link=%v mode=%v", content, info.Mode(), stat.Mode())
	}
}
