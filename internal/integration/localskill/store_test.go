package localskill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill 在 dir 下创建名为 folder 的技能文件夹并写入 SKILL.md。
func writeSkill(t *testing.T, dir, folder, content string) string {
	t.Helper()
	path := filepath.Join(dir, folder)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// testStore 创建以三个临时目录为技能目录的存储。
func testStore(t *testing.T) (*Store, []Dir) {
	t.Helper()
	dirs := []Dir{
		{Path: filepath.Join(t.TempDir(), "app"), Source: SourceManaged},
		{Path: filepath.Join(t.TempDir(), "agents"), Source: SourceAgents},
		{Path: filepath.Join(t.TempDir(), "claude"), Source: SourceClaude},
	}
	return NewStore(dirs, func() {}), dirs
}

// TestParseLenient 验证元数据的宽松解析：值中含冒号未加引号时补引号重试，CRLF 与 BOM 不影响解析。
func TestParseLenient(t *testing.T) {
	meta, body, err := parse([]byte("\xef\xbb\xbf---\r\nname: pdf\r\ndescription: Use this skill when: the user asks about PDFs\r\n---\r\n# PDF\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "pdf" || meta.Description != "Use this skill when: the user asks about PDFs" || body != "# PDF" {
		t.Fatalf("解析结果不符: %+v %q", meta, body)
	}
	if _, _, err := parse([]byte("# 没有元数据")); err == nil {
		t.Fatal("缺少元数据时应返回错误")
	}
}

// TestListPrecedenceAndSkips 验证重名时取优先级高的目录，缺少简介或无法解析的技能被跳过，名称缺失时使用文件夹名。
func TestListPrecedenceAndSkips(t *testing.T) {
	store, dirs := testStore(t)
	writeSkill(t, dirs[0].Path, "xlsx", "---\nname: xlsx\ndescription: 助理安装的表格技能\n---\n正文")
	writeSkill(t, dirs[1].Path, "xlsx", "---\nname: xlsx\ndescription: 被覆盖的表格技能\n---\n")
	writeSkill(t, dirs[1].Path, "broken", "---\nname: [broken\n---\n")
	writeSkill(t, dirs[1].Path, "empty", "---\nname: empty\n---\n")
	writeSkill(t, dirs[2].Path, "docx", "---\ndescription: 文档技能\n---\n")
	if err := os.WriteFile(filepath.Join(dirs[2].Path, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 || skills[0].Name != "docx" || skills[0].Source != SourceClaude ||
		skills[1].Name != "xlsx" || skills[1].Description != "助理安装的表格技能" || skills[1].Source != SourceManaged {
		t.Fatalf("技能列表不符: %+v", skills)
	}
	skill, body, err := store.Load(context.Background(), "xlsx")
	if err != nil || body != "正文" || skill.Dir != filepath.Join(dirs[0].Path, "xlsx") {
		t.Fatalf("读取技能不符: %+v %q %v", skill, body, err)
	}
}

// TestInstallFromLocalFolderAndRemove 验证从本机文件夹安装、同名替换，以及只删除助理安装目录中的技能。
func TestInstallFromLocalFolderAndRemove(t *testing.T) {
	store, dirs := testStore(t)
	source := t.TempDir()
	writeSkill(t, source, "skills/office/xlsx", "---\nname: xlsx\ndescription: 表格 v1\n---\n")
	writeSkill(t, source, "skills/office/docx", "---\nname: docx\ndescription: 文档\n---\n")
	if err := os.WriteFile(filepath.Join(source, "skills/office/xlsx/recalc.py"), []byte("print(1)"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Install(context.Background(), source, ""); err == nil || !strings.Contains(err.Error(), "docx") {
		t.Fatalf("多个技能未指定名称时应列出可选技能: %v", err)
	}
	skill, err := store.Install(context.Background(), source, "xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Dir != filepath.Join(dirs[0].Path, "xlsx") {
		t.Fatalf("安装位置不符: %s", skill.Dir)
	}
	if _, err := os.Stat(filepath.Join(skill.Dir, "recalc.py")); err != nil {
		t.Fatalf("附带文件未安装: %v", err)
	}
	writeSkill(t, source, "skills/office/xlsx", "---\nname: xlsx\ndescription: 表格 v2\n---\n")
	if skill, err = store.Install(context.Background(), filepath.Join(source, "skills/office/xlsx"), ""); err != nil || skill.Description != "表格 v2" {
		t.Fatalf("同名技能未被替换: %+v %v", skill, err)
	}
	writeSkill(t, dirs[1].Path, "shared", "---\nname: shared\ndescription: 其他工具安装\n---\n")
	if removed, err := store.Remove(context.Background(), "shared"); err != nil || removed {
		t.Fatalf("不应删除其他目录中的技能: %v %v", removed, err)
	}
	if removed, err := store.Remove(context.Background(), "xlsx"); err != nil || !removed {
		t.Fatalf("删除技能失败: %v %v", removed, err)
	}
	if _, err := os.Stat(skill.Dir); !os.IsNotExist(err) {
		t.Fatalf("技能文件夹仍存在: %v", err)
	}
}

// tarGz 生成包含指定文件的 tar.gz 内容。
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	for name, content := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		// 以 -> 开头的内容表示符号链接及其目标。
		if target, ok := strings.CutPrefix(content, "->"); ok {
			header, content = &tar.Header{Name: name, Linkname: target, Typeflag: tar.TypeSymlink}, ""
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// TestInstallFromGitHub 验证 GitHub 简写与目录地址下载仓库压缩包并取出其中的技能。
func TestInstallFromGitHub(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"skills-abc/skills/xlsx/SKILL.md": "---\nname: xlsx\ndescription: 表格\n---\n",
		"skills-abc/skills/pdf/SKILL.md":  "---\nname: pdf\ndescription: PDF\n---\n",
		"skills-abc/template/SKILL.md":    "---\nname: template\ndescription: 模板\n---\n",
		"skills-abc/skills/pdf/forms.md":  "表单",
		"skills-abc/README.md":            "说明",
	})
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	previous := githubArchiveURL
	githubArchiveURL = server.URL + "/%s/%s/archive/%s.tar.gz"
	defer func() { githubArchiveURL = previous }()

	store, dirs := testStore(t)
	skill, err := store.Install(context.Background(), "anthropics/skills", "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirs[0].Path, "pdf", "forms.md")); err != nil || skill.Name != "pdf" {
		t.Fatalf("技能未完整安装: %+v %v", skill, err)
	}
	if _, err := store.Install(context.Background(), "https://github.com/anthropics/skills/tree/main/skills/xlsx", ""); err != nil {
		t.Fatal(err)
	}
	if requested[0] != "/anthropics/skills/archive/HEAD.tar.gz" || requested[1] != "/anthropics/skills/archive/main.tar.gz" {
		t.Fatalf("下载地址不符: %v", requested)
	}
	skills, _ := store.List(context.Background())
	if len(skills) != 2 {
		t.Fatalf("应安装两个技能: %+v", skills)
	}
}

// TestFetchRoutesGitHubArchiveURL 验证 GitHub 上的发布附件与仓库压缩包地址按普通下载处理。
func TestFetchRoutesGitHubArchiveURL(t *testing.T) {
	store, _ := testStore(t)
	var requested []string
	store.client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.String())
		return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
	})}
	for _, address := range []string{
		"https://github.com/o/r/releases/download/v1/skill.zip",
		"https://github.com/o/r/archive/refs/heads/main.tar.gz",
	} {
		_, _ = store.Install(context.Background(), address, "")
	}
	if len(requested) != 2 || requested[0] != "https://github.com/o/r/releases/download/v1/skill.zip" ||
		requested[1] != "https://github.com/o/r/archive/refs/heads/main.tar.gz" {
		t.Fatalf("下载地址不符: %v", requested)
	}
}

// roundTripper 把函数适配为 HTTP 传输层。
type roundTripper func(*http.Request) (*http.Response, error)

// RoundTrip 调用函数处理请求。
func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestInstallFromSkillFileURL 验证地址内容为 SKILL.md 时安装为只含说明的技能。
func TestInstallFromSkillFileURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("---\nname: notes\ndescription: 记录\n---\n步骤"))
	}))
	defer server.Close()
	store, _ := testStore(t)
	if _, err := store.Install(context.Background(), server.URL+"/SKILL.md", ""); err != nil {
		t.Fatal(err)
	}
	if _, body, err := store.Load(context.Background(), "notes"); err != nil || body != "步骤" {
		t.Fatalf("技能内容不符: %q %v", body, err)
	}
}

// TestInstallDropsEscapingLinks 验证安装后解析到技能文件夹以外的符号链接被丢弃，指向技能内文件的保留。
func TestInstallDropsEscapingLinks(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"repo-abc/shared/secret.txt":    "共享",
		"repo-abc/skills/xlsx/SKILL.md": "---\nname: xlsx\ndescription: 表格\n---\n",
		"repo-abc/skills/xlsx/guide.md": "说明",
		"repo-abc/skills/xlsx/outside":  "->../../shared/secret.txt",
		"repo-abc/skills/xlsx/inside":   "->guide.md",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	store, dirs := testStore(t)
	if _, err := store.Install(context.Background(), server.URL+"/skills.tar.gz", ""); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dirs[0].Path, "xlsx")
	if _, err := os.Lstat(filepath.Join(dir, "outside")); !os.IsNotExist(err) {
		t.Fatalf("越界符号链接未被丢弃: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "inside")); err != nil || string(content) != "说明" {
		t.Fatalf("技能内的符号链接不可用: %q %v", content, err)
	}
}

// TestInstallRejectsHiddenName 验证以点开头的技能名称被拒绝，安装目录中不留下文件。
func TestInstallRejectsHiddenName(t *testing.T) {
	store, dirs := testStore(t)
	source := writeSkill(t, t.TempDir(), "hidden", "---\nname: .hidden\ndescription: 隐藏\n---\n")
	if _, err := store.Install(context.Background(), source, ""); err == nil {
		t.Fatal("以点开头的名称应被拒绝")
	}
	if entries, _ := os.ReadDir(dirs[0].Path); len(entries) != 0 {
		t.Fatalf("安装目录残留: %v", entries)
	}
}
