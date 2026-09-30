package localworkspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
)

// pngHeader 是最小 PNG 文件头，按内容识别为 image/png。
var pngHeader = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R'}

// newTestWorkspace 创建包含若干文件的默认文件夹与其外的目录，返回后端、默认文件夹与外部目录。
func newTestWorkspace(t *testing.T) (*Backend, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	files := map[string]string{
		"workspace/README.md":        "# Demo\n介绍\n",
		"workspace/src/main.go":      "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n",
		"workspace/src/util/text.go": "package util\n\n// Hello 返回问候。\nfunc Hello() string { return \"Hello\" }\n",
		"workspace/web/app.ts":       "export const hello = 'hello'\n",
		"workspace/.git/config":      "hello from git\n",
		"outside/notes.txt":          "outside hello\n",
	}
	for name, content := range files {
		path := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "logo.png"), pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.bin"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	return New(root, Environment{}), root, outside
}

// requireSymlink 创建符号链接，当前平台不支持时跳过测试。
func requireSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

// TestResolve 验证相对路径以默认文件夹为起点，绝对路径与 ~ 开头的路径访问本机任意位置，符号链接按目标读取，以目录链接为起点时 glob 与 grep 遍历其目标。
func TestResolve(t *testing.T) {
	ctx := context.Background()
	backend, root, outside := newTestWorkspace(t)
	if content, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "README.md"}); err != nil || content.Content != "# Demo\n介绍" {
		t.Fatalf("relative read=%+v %v", content, err)
	}
	if content, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: filepath.Join(outside, "notes.txt")}); err != nil || content.Content != "outside hello" {
		t.Fatalf("absolute read=%+v %v", content, err)
	}
	if content, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "../outside/notes.txt"}); err != nil || content.Content != "outside hello" {
		t.Fatalf("parent read=%+v %v", content, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := backend.resolve("~/Documents"); err != nil || got != filepath.Join(home, "Documents") {
		t.Fatalf("home path=%q %v", got, err)
	}
	requireSymlink(t, outside, filepath.Join(root, "linked"))
	if content, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "linked/notes.txt"}); err != nil || content.Content != "outside hello" {
		t.Fatalf("symlink read=%+v %v", content, err)
	}
	if infos, err := backend.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "*.txt", Path: "linked"}); err != nil || len(infos) != 1 || infos[0].Path != filepath.Join(root, "linked", "notes.txt") {
		t.Fatalf("glob in linked dir=%+v %v", infos, err)
	}
	if matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "outside", Path: "linked"}); err != nil || len(matches) != 1 || matches[0].Path != filepath.Join(root, "linked", "notes.txt") {
		t.Fatalf("grep in linked dir=%+v %v", matches, err)
	}
	if _, err := New(filepath.Join(t.TempDir(), "missing"), Environment{}).LsInfo(ctx, &filesystem.LsInfoRequest{}); err == nil {
		t.Fatal("listed missing default folder")
	}
}

// TestReadAndList 验证目录列表、按行读取、二进制文件、图片读取与写入拒绝。
func TestReadAndList(t *testing.T) {
	ctx := context.Background()
	backend, root, _ := newTestWorkspace(t)
	infos, err := backend.LsInfo(ctx, &filesystem.LsInfoRequest{Path: ""})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, info := range infos {
		rel, _ := filepath.Rel(root, info.Path)
		if info.IsDir {
			rel += "/"
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	if strings.Join(paths, ",") != ".git/,README.md,app.bin,logo.png,src/,web/" {
		t.Fatalf("ls=%v", paths)
	}
	content, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "src/main.go", Offset: 3, Limit: 2})
	if err != nil || content.Content != "func main() {\n\tprintln(\"hello\")" {
		t.Fatalf("read range=%+v %v", content, err)
	}
	if content, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "src/main.go", Offset: 100}); err != nil || content.Content != "" {
		t.Fatalf("read past end=%+v %v", content, err)
	}
	if _, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "app.bin"}); err == nil {
		t.Fatal("read binary file as text")
	}
	if _, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "src"}); err == nil {
		t.Fatal("read directory as file")
	}
	image, err := backend.MultiModalRead(ctx, &filesystem.MultiModalReadRequest{ReadRequest: filesystem.ReadRequest{FilePath: "logo.png"}})
	if err != nil || len(image.Parts) != 1 || image.Parts[0].MIMEType != "image/png" {
		t.Fatalf("read image=%+v %v", image, err)
	}
	text, err := backend.MultiModalRead(ctx, &filesystem.MultiModalReadRequest{ReadRequest: filesystem.ReadRequest{FilePath: "README.md"}})
	if err != nil || text.FileContent == nil || !strings.HasPrefix(text.Content, "# Demo") {
		t.Fatalf("multimodal read text=%+v %v", text, err)
	}
}

// TestWriteEditDelete 验证写入创建上级目录并覆盖已有文件，替换要求原文存在且唯一，删除只接受文件与空文件夹。
func TestWriteEditDelete(t *testing.T) {
	backend, root, _ := newTestWorkspace(t)
	ctx := context.Background()
	if err := backend.Write(ctx, &filesystem.WriteRequest{FilePath: "reports/q3.md", Content: "收入 100\n支出 100\n"}); err != nil {
		t.Fatal(err)
	}
	written := filepath.Join(root, "reports", "q3.md")
	if content, err := os.ReadFile(written); err != nil || string(content) != "收入 100\n支出 100\n" {
		t.Fatalf("written=%q %v", content, err)
	}
	if err := backend.Edit(ctx, &filesystem.EditRequest{FilePath: written, OldString: "100", NewString: "200"}); err == nil {
		t.Fatal("ambiguous edit accepted")
	}
	if err := backend.Edit(ctx, &filesystem.EditRequest{FilePath: written, OldString: "收入 100", NewString: "收入 300"}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Edit(ctx, &filesystem.EditRequest{FilePath: written, OldString: "100", NewString: "150", ReplaceAll: true}); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(written); string(content) != "收入 300\n支出 150\n" {
		t.Fatalf("edited=%q", content)
	}
	if err := backend.Edit(ctx, &filesystem.EditRequest{FilePath: "app.bin", OldString: "a", NewString: "c"}); err == nil {
		t.Fatal("binary edit accepted")
	}
	if err := backend.Delete(ctx, "src"); err == nil {
		t.Fatal("non-empty folder deleted")
	}
	if err := backend.Delete(ctx, written); err != nil {
		t.Fatal(err)
	}
	if err := backend.Delete(ctx, "reports"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "reports")); !os.IsNotExist(err) {
		t.Fatalf("reports still exists: %v", err)
	}
}

// TestSearch 验证 glob 与 grep 的匹配、过滤、上下文与跳过版本库目录。
func TestSearch(t *testing.T) {
	ctx := context.Background()
	backend, root, _ := newTestWorkspace(t)
	infos, err := backend.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "**/*.go"})
	if err != nil || len(infos) != 2 {
		t.Fatalf("glob=%+v %v", infos, err)
	}
	mainGo, appTS := filepath.Join(root, "src", "main.go"), filepath.Join(root, "web", "app.ts")
	if infos, err := backend.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "*.go", Path: "src"}); err != nil || len(infos) != 1 || infos[0].Path != mainGo {
		t.Fatalf("glob in dir=%+v %v", infos, err)
	}
	if infos, err := backend.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: filepath.Join(root, "web", "*.ts"), Path: "src"}); err != nil || len(infos) != 1 || infos[0].Path != appTS {
		t.Fatalf("absolute glob=%+v %v", infos, err)
	}
	if _, err := backend.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "["}); err == nil {
		t.Fatal("invalid glob accepted")
	}

	matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "hello", CaseInsensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, match := range matches {
		files = append(files, match.Path)
	}
	joined := strings.Join(files, ",")
	if strings.Contains(joined, ".git") || !strings.Contains(joined, filepath.Join(root, "src", "util", "text.go")) || !strings.Contains(joined, appTS) {
		t.Fatalf("grep files=%v", files)
	}
	if matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "hello", Glob: "*.go"}); err != nil || len(matches) != 1 || matches[0].Path != mainGo || matches[0].Line != 4 {
		t.Fatalf("grep glob=%+v %v", matches, err)
	}
	if matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "hello", FileType: "ts"}); err != nil || len(matches) != 1 || matches[0].Path != appTS {
		t.Fatalf("grep type=%+v %v", matches, err)
	}
	if matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "println", Path: "src/main.go", BeforeLines: 1, AfterLines: 1}); err != nil || len(matches) != 3 || matches[0].Line != 3 || matches[2].Line != 5 {
		t.Fatalf("grep context=%+v %v", matches, err)
	}
	if matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: `main\(\) \{.*println`, EnableMultiline: true}); err != nil || len(matches) != 2 || matches[0].Line != 3 || matches[1].Line != 4 {
		t.Fatalf("grep multiline=%+v %v", matches, err)
	}
	// 搜索单个文件时过滤条件按文件名匹配。
	if matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "println", Path: "src/main.go", Glob: "*.go", FileType: "go"}); err != nil || len(matches) != 1 {
		t.Fatalf("grep single file with filters=%+v %v", matches, err)
	}
	if _, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "(?<=a)b"}); err == nil {
		t.Fatal("unsupported regexp accepted")
	}
}

// TestConcurrentEditsAndReplace 验证并行修改同一文件的改动全部保留，覆盖时保留原权限且符号链接写入目标。
func TestConcurrentEditsAndReplace(t *testing.T) {
	backend, root, _ := newTestWorkspace(t)
	ctx := context.Background()
	file := filepath.Join(root, "list.txt")
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("item-%02d", i)
	}
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := range lines {
		group.Go(func() {
			if err := backend.Edit(ctx, &filesystem.EditRequest{FilePath: file, OldString: lines[i], NewString: lines[i] + "-done"}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	content, _ := os.ReadFile(file)
	if strings.Count(string(content), "-done") != len(lines) {
		t.Fatalf("content=%q", content)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
	link := filepath.Join(root, "link.txt")
	requireSymlink(t, file, link)
	if err := backend.Write(ctx, &filesystem.WriteRequest{FilePath: "link.txt", Content: "replaced"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v", err)
	}
	if content, _ := os.ReadFile(file); string(content) != "replaced" {
		t.Fatalf("target=%q", content)
	}
	// 目标尚不存在的链接写入后创建目标，链接本身保留。
	dangling := filepath.Join(root, "alias.txt")
	requireSymlink(t, "target.txt", dangling)
	if err := backend.Write(ctx, &filesystem.WriteRequest{FilePath: "alias.txt", Content: "created"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(dangling); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("dangling link replaced: %v", err)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "target.txt")); string(content) != "created" {
		t.Fatalf("dangling target=%q", content)
	}
	// 所在目录也是链接时，链接中的 .. 相对真实目录解析。
	if err := os.MkdirAll(filepath.Join(root, "real", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, filepath.Join("real", "sub"), filepath.Join(root, "aliasdir"))
	requireSymlink(t, filepath.Join("..", "target.txt"), filepath.Join(root, "real", "sub", "file.txt"))
	if err := backend.Write(ctx, &filesystem.WriteRequest{FilePath: "aliasdir/file.txt", Content: "nested"}); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "real", "target.txt")); string(content) != "nested" {
		t.Fatalf("nested target=%q", content)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "target.txt")); string(content) != "created" {
		t.Fatalf("root target overwritten=%q", content)
	}
}
