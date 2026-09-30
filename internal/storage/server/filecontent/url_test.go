//go:build server

package filecontent

import (
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
)

// TestPublicURL 验证公开基础地址会保留路径并拒绝越界对象键。
func TestPublicURL(t *testing.T) {
	key := "organizations/org/files/file.png"
	value, err := PublicURL("https://cdn.example.com/assets/", key)
	if err != nil || value != "https://cdn.example.com/assets/organizations/org/files/file.png" {
		t.Fatalf("PublicURL() = %q, %v", value, err)
	}
	local, err := PublicURL("/storage", key)
	if err != nil || local != "/storage/organizations/org/files/file.png" {
		t.Fatalf("local PublicURL() = %q, %v", local, err)
	}
	for _, invalid := range []string{"../file.png", "/file.png", `organizations\\file.png`} {
		if _, err := PublicURL("https://cdn.example.com", invalid); err == nil {
			t.Fatalf("expected key %q to fail", invalid)
		}
	}
}

// TestLinksURL 验证本地存储地址拼接部署地址，对象存储地址使用公开基础地址。
func TestLinksURL(t *testing.T) {
	links := NewLinks("https://app.example.com/", "https://cdn.example.com/assets")
	key := "organizations/org/files/file.png"
	for backend, want := range map[domain.FileStorageBackend]string{
		domain.FileStorageBackendLocal: "https://app.example.com/storage/" + key,
		domain.FileStorageBackendS3:    "https://cdn.example.com/assets/" + key,
	} {
		if got, err := links.URL(backend, key); err != nil || got != want {
			t.Fatalf("%s URL() = %q, %v, want %q", backend, got, err, want)
		}
	}
	if _, err := links.URL("unknown", key); err == nil {
		t.Fatal("expected unknown storage backend to fail")
	}
}
