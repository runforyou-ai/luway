package clientrelease

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
)

// abcSHA512 是内容 abc 的 SHA-512 摘要。
const abcSHA512 = "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"

// abcFile 返回内容为 abc 的客户端文件。
func abcFile(name string) File {
	return File{Name: name, Size: 3, SHA512: abcSHA512}
}

// writeDirectory 在临时目录写入索引与索引中内容为 abc 的文件。
func writeDirectory(t *testing.T, index Index) string {
	t.Helper()
	directory := t.TempDir()
	names := []string{}
	for _, installer := range index.Installers {
		names = append(names, installer.Name)
	}
	for _, update := range index.Updates {
		names = append(names, update.Name)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(directory, filepath.Base(name)), []byte("abc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(directory, IndexName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return directory
}

// TestLoad 校验目录缺少索引或版本不同时不提供客户端，大小或摘要不符、文件名重复或越出目录、同一平台更新包重复时报错。
func TestLoad(t *testing.T) {
	installer := Installer{OS: OSLinux, Arch: "amd64", Format: FormatDeb, File: abcFile("app_1.0.0_linux_amd64.deb")}
	update := Update{OS: OSWindows, Arch: "amd64", File: abcFile("app_1.0.0_windows_amd64.exe")}
	empty, err := Load(t.TempDir(), "1.0.0")
	if err != nil || len(empty.Installers()) != 0 || empty.Version() != "" {
		t.Fatalf("缺少索引时 = %+v, %v", empty, err)
	}
	other, err := Load(writeDirectory(t, Index{Version: "0.9.0", Installers: []Installer{installer}}), "1.0.0")
	if err != nil || len(other.Installers()) != 0 || other.Version() != "" {
		t.Fatalf("版本不同时 = %+v, %v", other, err)
	}
	loaded, err := Load(writeDirectory(t, Index{Version: "1.0.0", Installers: []Installer{installer}, Updates: []Update{update}}), "1.0.0")
	if err != nil || len(loaded.Installers()) != 1 || loaded.Version() != "1.0.0" {
		t.Fatalf("版本一致时 = %+v, %v", loaded, err)
	}
	wrongSize := installer
	wrongSize.Size = 4
	wrongDigest := update
	wrongDigest.SHA512 = abcSHA512[1:] + "0"
	duplicateUpdate := update
	duplicateUpdate.Name = "app_1.0.0_windows_amd64-2.exe"
	invalid := map[string]Index{
		"大小不符":       {Version: "1.0.0", Installers: []Installer{wrongSize}},
		"摘要不符":       {Version: "1.0.0", Updates: []Update{wrongDigest}},
		"文件名重复":      {Version: "1.0.0", Installers: []Installer{installer, installer}},
		"文件名与清单路径重名": {Version: "1.0.0", Installers: []Installer{{OS: OSLinux, Arch: "amd64", Format: FormatDeb, File: abcFile("update")}}},
		"同一平台更新包重复":  {Version: "1.0.0", Updates: []Update{update, duplicateUpdate}},
		"文件名越出目录":    {Version: "1.0.0", Installers: []Installer{{OS: OSLinux, Arch: "amd64", Format: FormatDeb, File: abcFile("../app.deb")}}},
	}
	for name, index := range invalid {
		if _, err := Load(writeDirectory(t, index), "1.0.0"); err == nil {
			t.Errorf("%s时应报错", name)
		}
	}
}

// TestServeHTTP 校验只输出索引中登记的文件并以附件形式下载。
func TestServeHTTP(t *testing.T) {
	file := abcFile("app_1.0.0_windows_amd64-installer.exe")
	update := Update{OS: OSWindows, Arch: "amd64", File: abcFile("app_1.0.0_windows_amd64.exe")}
	directory := writeDirectory(t, Index{Version: "1.0.0", Installers: []Installer{{OS: OSWindows, Arch: "amd64", Format: FormatEXE, File: file}}, Updates: []Update{update}})
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
		{http.MethodGet, URL(update.File), http.StatusOK},
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

// TestServeManifest 校验更新清单只在客户端版本较低时返回当前平台的更新包，macOS 通用更新包匹配任意架构，所有响应都禁止缓存。
func TestServeManifest(t *testing.T) {
	updates := []Update{
		{OS: OSWindows, Arch: "amd64", File: abcFile("app_1.2.0_windows_amd64.exe"), Signature: "c2lnbmF0dXJl"},
		{OS: OSDarwin, Arch: ArchUniversal, File: abcFile("app_1.2.0_darwin_universal.zip"), Signature: "c2lnbmF0dXJl"},
	}
	catalog, err := Load(writeDirectory(t, Index{Version: "1.2.0", Updates: updates}), "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		query  string
		status int
		name   string
	}{
		{"platform=windows&arch=amd64&version=1.1.0", http.StatusOK, updates[0].Name},
		{"platform=darwin&arch=arm64&version=1.1.9", http.StatusOK, updates[1].Name},
		{"platform=windows&arch=amd64&version=1.2.0", http.StatusNoContent, ""},
		{"platform=windows&arch=amd64&version=1.3.0", http.StatusNoContent, ""},
		{"platform=windows&arch=arm64&version=1.1.0", http.StatusNotFound, ""},
		{"platform=linux&arch=amd64&version=1.1.0", http.StatusNotFound, ""},
		{"platform=windows&arch=amd64&version=dev", http.StatusBadRequest, ""},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		catalog.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, domain.ClientUpdatePath+"?"+test.query, nil))
		if recorder.Code != test.status || recorder.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s 状态码 = %d、Cache-Control = %q，期望 %d、no-cache", test.query, recorder.Code, recorder.Header().Get("Cache-Control"), test.status)
			continue
		}
		if test.status != http.StatusOK {
			continue
		}
		var document manifest
		if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil || document.Version != "1.2.0" || len(document.Artifacts) != 1 {
			t.Fatalf("%s 清单 = %s, %v", test.query, recorder.Body.String(), err)
		}
		artifact := document.Artifacts[0]
		if artifact.Filename != test.name || artifact.URL != PathPrefix+test.name+"?v="+abcSHA512[:16] || artifact.DigestAlgo != "sha512" || artifact.SignatureAlgo != "ed25519ph" || artifact.Signature != "c2lnbmF0dXJl" {
			t.Errorf("%s 更新包 = %+v", test.query, artifact)
		}
	}
	empty, _ := Load(t.TempDir(), "1.2.0")
	recorder := httptest.NewRecorder()
	empty.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, domain.ClientUpdatePath+"?platform=windows&arch=amd64&version=1.0.0", nil))
	if recorder.Code != http.StatusNoContent {
		t.Errorf("没有客户端时状态码 = %d，期望 204", recorder.Code)
	}
}
