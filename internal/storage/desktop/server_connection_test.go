//go:build !server && !ios && !android

package desktop

import (
	"context"
	"path/filepath"
	"testing"
)

// TestServerURLPersistsInDesktopStorage 验证桌面端企业服务器地址能够持久化。
func TestServerURLPersistsInDesktopStorage(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "app.db")
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}

	serverURL, err := store.GetServerURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if serverURL != "" {
		t.Fatalf("initial server URL = %q, want empty", serverURL)
	}

	const expected = "https://app.example.com"
	if err := store.SetServerURL(context.Background(), expected); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	serverURL, err = store.GetServerURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if serverURL != expected {
		t.Fatalf("server URL = %q, want %q", serverURL, expected)
	}
}
