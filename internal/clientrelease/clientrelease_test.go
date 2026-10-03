package clientrelease

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
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
	names := []string{}
	for _, file := range index.Files {
		names = append(names, file.Name)
	}
	for _, update := range index.Updates {
		names = append(names, update.Name)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("abc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(directory, IndexName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return directory
}

// signedUpdate 返回内容为 abc、用 key 为 version 签名的更新包。
func signedUpdate(key ed25519.PrivateKey, version, system, arch, name string) Update {
	digest := sha512.Sum512([]byte("abc"))
	signature := ed25519.Sign(key, UpdateStatement(version, digest[:]))
	return Update{OS: system, Arch: arch, Name: name, Size: 3, SHA256: abcSHA256, Signature: base64.StdEncoding.EncodeToString(signature)}
}

// TestLoad 校验目录缺少索引或版本不同时不提供安装包，大小或摘要不符、文件名重复或越出目录时报错。
func TestLoad(t *testing.T) {
	file := File{OS: OSLinux, Arch: "amd64", Format: FormatDeb, Name: "app_1.0.0_linux_amd64.deb", Size: 3, SHA256: abcSHA256}
	empty, err := Load(t.TempDir(), "1.0.0", nil)
	if err != nil || len(empty.Files()) != 0 || empty.Version() != "" {
		t.Fatalf("缺少索引时 = %+v, %v", empty, err)
	}
	other, err := Load(writeDirectory(t, Index{Version: "0.9.0", Files: []File{file}}), "1.0.0", nil)
	if err != nil || len(other.Files()) != 0 {
		t.Fatalf("版本不同时 = %+v, %v", other, err)
	}
	loaded, err := Load(writeDirectory(t, Index{Version: "1.0.0", Files: []File{file}}), "1.0.0", nil)
	if err != nil || len(loaded.Files()) != 1 || loaded.Version() != "1.0.0" {
		t.Fatalf("版本一致时 = %+v, %v", loaded, err)
	}
	wrongSize := file
	wrongSize.Size = 4
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Files: []File{wrongSize}}), "1.0.0", nil); err == nil {
		t.Error("大小不符时应报错")
	}
	wrongDigest := file
	wrongDigest.SHA256 = abcSHA256[1:] + "0"
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Files: []File{wrongDigest}}), "1.0.0", nil); err == nil {
		t.Error("摘要不符时应报错")
	}
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Files: []File{file, file}}), "1.0.0", nil); err == nil {
		t.Error("文件名重复时应报错")
	}
	directory := writeDirectory(t, Index{Version: "1.0.0", Files: []File{file}})
	data, _ := json.Marshal(Index{Version: "1.0.0", Files: []File{{Name: "../" + file.Name, Size: 3}}})
	if err := os.WriteFile(filepath.Join(directory, IndexName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(directory, "1.0.0", nil); err == nil {
		t.Error("文件名越出目录时应报错")
	}
}

// TestServeHTTP 校验只输出索引中登记的安装包并以附件形式下载。
func TestServeHTTP(t *testing.T) {
	file := File{OS: OSWindows, Arch: "amd64", Format: FormatEXE, Name: "app_1.0.0_windows_amd64-installer.exe", Size: 3, SHA256: abcSHA256}
	directory := writeDirectory(t, Index{Version: "1.0.0", Files: []File{file}})
	catalog, err := Load(directory, "1.0.0", nil)
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

// TestLoadVerifiesUpdateSignature 校验更新包签名须由品牌公钥验证通过，文件名不能与更新清单路径冲突。
func TestLoadVerifiesUpdateSignature(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	update := signedUpdate(private, "1.0.0", OSDarwin, "universal", "app_1.0.0_darwin_universal.zip")
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Updates: []Update{update}}), "1.0.0", public); err != nil {
		t.Fatalf("签名有效时应读取成功: %v", err)
	}
	forged := signedUpdate(other, "1.0.0", OSDarwin, "universal", update.Name)
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Updates: []Update{forged}}), "1.0.0", public); err == nil {
		t.Error("其他密钥签名时应报错")
	}
	// 旧版本的签名不能用于新版本号。
	older := signedUpdate(private, "0.9.0", OSDarwin, "universal", update.Name)
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Updates: []Update{older}}), "1.0.0", public); err == nil {
		t.Error("签名版本与目录版本不同时应报错")
	}
	unsigned := update
	unsigned.Signature = ""
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Updates: []Update{unsigned}}), "1.0.0", public); err == nil {
		t.Error("缺少签名时应报错")
	}
	reserved := signedUpdate(private, "1.0.0", OSDarwin, "universal", "update")
	if _, err := Load(writeDirectory(t, Index{Version: "1.0.0", Updates: []Update{reserved}}), "1.0.0", public); err == nil {
		t.Error("文件名与更新清单路径冲突时应报错")
	}
}

// TestServeUpdate 校验更新清单按平台与架构给出对应更新包，通用架构匹配任意架构，下载地址带内容摘要；没有对应更新包时清单只给出版本，服务器不提供安装包时返回 204。
func TestServeUpdate(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	darwin := signedUpdate(private, "1.0.0", OSDarwin, "universal", "app_1.0.0_darwin_universal.zip")
	windows := signedUpdate(private, "1.0.0", OSWindows, "arm64", "app_1.0.0_windows_arm64.zip")
	installer := File{OS: OSLinux, Arch: "amd64", Format: FormatDeb, Name: "app_1.0.0_linux_amd64.deb", Size: 3, SHA256: abcSHA256}
	catalog, err := Load(writeDirectory(t, Index{Version: "1.0.0", Files: []File{installer}, Updates: []Update{darwin, windows}}), "1.0.0", public)
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]string{
		UpdatePath + "?platform=darwin&arch=arm64&version=0.9.0":  darwin.Name,
		UpdatePath + "?platform=windows&arch=arm64&version=0.9.0": windows.Name,
		UpdatePath + "?platform=windows&arch=amd64&version=0.9.0": "",
		UpdatePath + "?platform=linux&arch=amd64&version=0.9.0":   "",
	} {
		recorder := httptest.NewRecorder()
		catalog.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		var manifest updateManifest
		if err := json.NewDecoder(recorder.Body).Decode(&manifest); err != nil || manifest.Version != "1.0.0" {
			t.Fatalf("%s 清单 = %+v, %v", target, manifest, err)
		}
		if want == "" {
			if len(manifest.Artifacts) != 0 {
				t.Errorf("%s 不应含更新包: %+v", target, manifest.Artifacts)
			}
			continue
		}
		if len(manifest.Artifacts) != 1 {
			t.Fatalf("%s 更新包数量 = %d", target, len(manifest.Artifacts))
		}
		if artifact := manifest.Artifacts[0]; artifact.URL != want+"?v="+abcSHA256[:16] || artifact.DigestAlgo != "sha512" || artifact.Digest == "" || manifest.Metadata[SignatureMetadataKey] == "" {
			t.Errorf("%s 更新包 = %+v", target, artifact)
		}
	}
	recorder := httptest.NewRecorder()
	catalog.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, PathPrefix+darwin.Name+"?v="+abcSHA256[:16], nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "abc" {
		t.Errorf("下载更新包 = %d, %q", recorder.Code, recorder.Body.String())
	}
	empty, _ := Load(t.TempDir(), "1.0.0", public)
	recorder = httptest.NewRecorder()
	empty.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, UpdatePath+"?platform=linux&arch=amd64", nil))
	if recorder.Code != http.StatusNoContent {
		t.Errorf("服务器不提供客户端时状态码 = %d，期望 204", recorder.Code)
	}
}
