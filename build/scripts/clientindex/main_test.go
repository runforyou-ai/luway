package main

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/brand"
)

// writeArtifacts 在临时目录写入版本对应的全部发布产物，macOS 更新包是含给定条目的 zip，返回该目录。
func writeArtifacts(t *testing.T, version string, bundleEntries ...string) string {
	t.Helper()
	source := t.TempDir()
	prefix := brand.Build().Slug + "_" + version + "_"
	for _, suffix := range []string{
		"windows_amd64-installer.exe", "windows_arm64-installer.exe", "darwin_universal.dmg", "linux_amd64.AppImage",
		"linux_amd64.deb", "linux_amd64.rpm", "linux_amd64.pkg.tar.zst",
		"windows_amd64.exe", "windows_arm64.exe",
	} {
		if err := os.WriteFile(filepath.Join(source, prefix+suffix), []byte(suffix), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(bundleEntries) == 0 {
		bundleEntries = []string{"app.app/Contents/Info.plist", "app.app/Contents/MacOS/app"}
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range bundleEntries {
		entry, _ := writer.Create(name)
		_, _ = entry.Write([]byte(name))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, prefix+"darwin_universal.zip"), archive.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return source
}

// TestRunSignsUpdates 校验生成的客户端目录能被服务端加载，更新包签名可用品牌公钥按 Ed25519ph 验证，macOS 更新包根目录只能含一个应用包。
func TestRunSignsUpdates(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(privateKey)
	keyPath := filepath.Join(t.TempDir(), "updater.key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	if err := run("1.2.0", writeArtifacts(t, "1.2.0"), output, keyPath, publicKey); err != nil {
		t.Fatal(err)
	}
	catalog, err := clientrelease.Load(output, "1.2.0")
	if err != nil || len(catalog.Installers()) != 7 {
		t.Fatalf("加载客户端目录 = %v, %v", catalog, err)
	}
	var index struct{ Updates []clientrelease.Update }
	data, _ := os.ReadFile(filepath.Join(output, clientrelease.IndexName))
	if err := json.Unmarshal(data, &index); err != nil || len(index.Updates) != 3 {
		t.Fatalf("更新包 = %+v, %v", index.Updates, err)
	}
	for _, update := range index.Updates {
		digest, _ := hex.DecodeString(update.SHA512)
		signature, _ := base64.StdEncoding.DecodeString(update.Signature)
		if err := ed25519.VerifyWithOptions(publicKey, digest, signature, &ed25519.Options{Hash: crypto.SHA512}); err != nil {
			t.Errorf("%s 签名验证失败: %v", update.Name, err)
		}
	}
	otherPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := run("1.2.0", writeArtifacts(t, "1.2.0"), t.TempDir(), keyPath, otherPublicKey); err == nil {
		t.Error("私钥与品牌公钥不配对时应报错")
	}
	if err := run("1.2.0", writeArtifacts(t, "1.2.0"), t.TempDir(), "", publicKey); err == nil {
		t.Error("品牌配置了公钥但未传私钥时应报错")
	}
	if err := run("1.2.0", writeArtifacts(t, "1.2.0", "app.app/Contents/MacOS/app", "__MACOSX/app.app/._Contents"), t.TempDir(), keyPath, publicKey); err == nil {
		t.Error("macOS 更新包根目录含多项时应报错")
	}
}

// TestRunWithoutUpdateKey 校验品牌未配置更新公钥时只收录安装包，缺少任一安装包时报错。
func TestRunWithoutUpdateKey(t *testing.T) {
	source := writeArtifacts(t, "1.2.0")
	output := t.TempDir()
	if err := run("1.2.0", source, output, "", nil); err != nil {
		t.Fatal(err)
	}
	var index clientrelease.Index
	data, _ := os.ReadFile(filepath.Join(output, clientrelease.IndexName))
	if err := json.Unmarshal(data, &index); err != nil || len(index.Installers) != 7 || len(index.Updates) != 0 {
		t.Fatalf("索引 = %+v, %v", index, err)
	}
	if err := os.Remove(filepath.Join(source, brand.Build().Slug+"_1.2.0_linux_amd64.rpm")); err != nil {
		t.Fatal(err)
	}
	if err := run("1.2.0", source, t.TempDir(), "", nil); err == nil {
		t.Error("缺少安装包时应报错")
	}
}
