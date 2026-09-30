package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// tarEntry 是测试压缩包中的一个条目。
type tarEntry struct {
	name     string
	content  string
	linkname string
}

// writeTarGz 把条目写成 tar.gz 文件并返回路径。
func writeTarGz(t *testing.T, entries []tarEntry) string {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.content)), Typeflag: tar.TypeReg}
		if entry.linkname != "" {
			header = &tar.Header{Name: entry.name, Linkname: entry.linkname, Typeflag: tar.TypeSymlink}
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestExtractTarGzRejectsEscapingEntries 验证越出解压目录的文件与符号链接被拒绝。
func TestExtractTarGzRejectsEscapingEntries(t *testing.T) {
	for _, entries := range [][]tarEntry{
		{{name: "../escape", content: "x"}},
		{{name: "bin/link", linkname: "../../outside"}},
		{{name: "bin/link", linkname: "/etc/passwd"}},
		// 经符号链接写出解压目录。
		{{name: "a", linkname: "."}, {name: "a/b", linkname: ".."}, {name: "a/b/escape", content: "x"}},
		// 符号链接串联后解析到解压目录外。
		{{name: "d/f", content: "x"}, {name: "p/x/q", linkname: "../../d"}, {name: "p/x/link", linkname: "q/../../.."}},
	} {
		if err := ExtractTarGz(writeTarGz(t, entries), t.TempDir()); err == nil {
			t.Fatalf("未拒绝条目 %+v", entries)
		}
	}
}

// TestExtractTarGzKeepsInternalSymlinks 验证指向解压目录内的符号链接照常创建。
func TestExtractTarGzKeepsInternalSymlinks(t *testing.T) {
	root := t.TempDir()
	path := writeTarGz(t, []tarEntry{{name: "lib/cli.js", content: "x"}, {name: "bin/npm", linkname: "../lib/cli.js"}})
	if err := ExtractTarGz(path, root); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "bin", "npm")); err != nil || string(content) != "x" {
		t.Fatalf("符号链接不可用: %q %v", content, err)
	}
}

// TestExtractZipRejectsEscapingEntries 验证 zip 中越出解压目录的条目被拒绝，正常条目按目录结构解压。
func TestExtractZipRejectsEscapingEntries(t *testing.T) {
	build := func(name string) string {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "archive.zip")
		if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := ExtractZip(build("../escape"), t.TempDir()); err == nil {
		t.Fatal("未拒绝越出解压目录的条目")
	}
	root := t.TempDir()
	if err := ExtractZip(build("dir/file.txt"), root); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "dir", "file.txt")); err != nil || string(content) != "x" {
		t.Fatalf("解压内容不符: %q %v", content, err)
	}
}
