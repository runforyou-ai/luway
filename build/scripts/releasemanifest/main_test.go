package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/release"
	releaseclients "github.com/runforyou-ai/luway/internal/release/clients"
)

// TestReleaseArchitectures 验证发布矩阵与产物摘要、服务端可直接加载生成的目录，旧架构文件不再是发布前提。
func TestReleaseArchitectures(t *testing.T) {
	source, output := t.TempDir(), t.TempDir()
	version := "1.2.3"
	slug := brand.Build().Slug
	names := []string{
		slug + "-server_1.2.3_linux_amd64.tar.gz", slug + "-server_1.2.3_linux_arm64.tar.gz",
		slug + "-executor_1.2.3_linux_amd64.tar.gz", slug + "-executor_1.2.3_linux_arm64.tar.gz",
		slug + "-executor_1.2.3_windows_amd64.zip", slug + "-executor_1.2.3_darwin_arm64.tar.gz",
		slug + "_1.2.3_windows_amd64-installer.exe", slug + "_1.2.3_windows_amd64.zip",
		slug + "_1.2.3_darwin_arm64.dmg", slug + "_1.2.3_darwin_arm64.zip",
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, ext := range []string{"AppImage", "deb", "rpm", "pkg.tar.zst"} {
			names = append(names, slug+"_1.2.3_linux_"+arch+"."+ext)
		}
	}
	payload := []byte("release fixture")
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(source, name), payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(version, source, output, "windows,darwin,linux"); err != nil {
		t.Fatal(err)
	}
	manifest, err := release.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Servers) != 2 || len(manifest.Installers) != 10 || len(manifest.Updates) != 2 || len(manifest.Executors) != 4 {
		t.Fatalf("unexpected release matrix: %+v", manifest)
	}
	for _, server := range manifest.Servers {
		if server.OS != "linux" {
			t.Fatalf("unexpected server: %+v", server)
		}
	}
	counts := map[string]int{}
	digest := sha256.Sum256(payload)
	for _, installer := range manifest.Installers {
		counts[installer.OS+"/"+installer.Arch]++
		if installer.Size != int64(len(payload)) || installer.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("invalid artifact digest: %+v", installer)
		}
	}
	for platform, count := range map[string]int{"windows/amd64": 1, "darwin/arm64": 1, "linux/amd64": 4, "linux/arm64": 4} {
		if counts[platform] != count {
			t.Errorf("%s: got %d, want %d", platform, counts[platform], count)
		}
	}
	for _, update := range manifest.Updates {
		if update.OS == "darwin" && update.Arch != "arm64" {
			t.Fatalf("unexpected update: %+v", update)
		}
	}
	if _, err := os.Stat(filepath.Join(output, "release.json.sig")); !os.IsNotExist(err) {
		t.Fatalf("release manifest must not be signed: %v", err)
	}
	catalog, err := releaseclients.Load(output, version)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version() != version {
		t.Fatalf("catalog version: got %q, want %q", catalog.Version(), version)
	}
	if err := os.Remove(filepath.Join(source, slug+"_1.2.3_linux_arm64.deb")); err != nil {
		t.Fatal(err)
	}
	if err := run(version, source, t.TempDir(), "windows,darwin,linux"); err == nil {
		t.Fatal("missing ARM64 package must fail the release")
	}
}
