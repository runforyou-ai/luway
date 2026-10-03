package clientrelease

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// abcSHA256 是内容 abc 的 SHA-256 摘要。
const abcSHA256 = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

// writeDirectory 在临时目录写入索引与内容为 abc 的安装包。
func writeDirectory(t *testing.T, index Index) string {
	t.Helper()
	directory := t.TempDir()
	for _, file := range index.Files {
		if err := os.WriteFile(filepath.Join(directory, file.Name), []byte("abc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(directory, IndexName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return directory
}

// TestLoad 校验目录缺少索引或版本不同时不提供安装包，大小或摘要不符、文件名重复或越出目录时报错。
func TestLoad(t *testing.T) {
	file := File{OS: OSLinux, Arch: "amd64", Format: FormatDeb, Name: "app_1.0.0_linux_amd64.deb", Size: 3, SHA256: abcSHA256}
	empty, err := Load(t.TempDir(), "1.0.0")
	if err != nil || len(empty.Files()) != 0 || empty.Version() != "" {
		t.Fatalf("缺少索引时 = %+v, %v", empty, err)
	}
	other, err := Load(writeDirectory(t, Index{Version: "0.9.0", Files: []File{file}}), "1.0.0")
	if err != nil || len(other.Files()) != 0 {
		t.Fatalf("版本不同时 = %+v, %v", other, err)
	}
	loaded, err := Load(writeDirectory(t, Index{Version: "1.0.0", Files: []File{file}}), "1.0.0")
	if err != nil || len(loaded.Files()) != 1 || loaded.Version() != "1.0.0" {
		t.Fatalf("版本一致时 = %+v, %v", loaded, err)
	}
	wrongSize := file
	wrongSize.Size = 4
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Files: []File{wrongSize}}), "1.0.0"); err == nil {
		t.Error("大小不符时应报错")
	}
	wrongDigest := file
	wrongDigest.SHA256 = abcSHA256[1:] + "0"
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Files: []File{wrongDigest}}), "1.0.0"); err == nil {
		t.Error("摘要不符时应报错")
	}
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Files: []File{file, file}}), "1.0.0"); err == nil {
		t.Error("文件名重复时应报错")
	}
	directory := writeDirectory(t, Index{Version: "1.0.0", Files: []File{file}})
	data, _ := json.Marshal(Index{Version: "1.0.0", Files: []File{{Name: "../" + file.Name, Size: 3}}})
	if err := os.WriteFile(filepath.Join(directory, IndexName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(directory, "1.0.0"); err == nil {
		t.Error("文件名越出目录时应报错")
	}
}

// TestServeHTTP 校验只输出索引中登记的安装包并以附件形式下载。
func TestServeHTTP(t *testing.T) {
	file := File{OS: OSWindows, Arch: "amd64", Format: FormatEXE, Name: "app_1.0.0_windows_amd64-installer.exe", Size: 3, SHA256: abcSHA256}
	directory := writeDirectory(t, Index{Version: "1.0.0", Files: []File{file}})
	catalog, err := Load(directory, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method, target string
		status         int
	}{
		{http.MethodGet, URL(file), http.StatusOK},
		{http.MethodHead, URL(file), http.StatusOK},
		{http.MethodGet, PathPrefix + IndexName, http.StatusNotFound},
		{http.MethodGet, PathPrefix + "missing.exe", http.StatusNotFound},
		{http.MethodPost, URL(file), http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		catalog.ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
		if recorder.Code != test.status {
			t.Errorf("%s %s 状态码 = %d，期望 %d", test.method, test.target, recorder.Code, test.status)
		}
	}
	recorder := httptest.NewRecorder()
	catalog.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, URL(file), nil))
	if recorder.Body.String() != "abc" || recorder.Header().Get("Content-Disposition") != `attachment; filename="`+file.Name+`"` {
		t.Errorf("下载响应 = %q, %q", recorder.Body.String(), recorder.Header().Get("Content-Disposition"))
	}
}
