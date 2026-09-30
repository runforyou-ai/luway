//go:build !server && !ios && !android

package desktop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/runforyou-ai/cervi/internal/clientsession"
)

// TestClientSessionPersistsInDesktopStorage 验证桌面端登录凭据能够持久化和删除。
func TestClientSessionPersistsInDesktopStorage(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "app.db")
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}

	if _, found, err := store.LoadClientSession(context.Background()); err != nil || found {
		t.Fatalf("initial client session found = %v, error = %v", found, err)
	}
	expected := clientsession.Credential{
		ServerURL: "https://app.example.com",
		AccountID: "account-1",
		Token:     "test-token",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := store.SaveClientSession(context.Background(), expected); err != nil {
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
	actual, found, err := store.LoadClientSession(context.Background())
	if err != nil || !found {
		t.Fatalf("persisted client session found = %v, error = %v", found, err)
	}
	if actual.ServerURL != expected.ServerURL || actual.AccountID != expected.AccountID || actual.Token != expected.Token || !actual.ExpiresAt.Equal(expected.ExpiresAt) {
		t.Fatalf("client session = %#v, want %#v", actual, expected)
	}
	if err := store.DeleteClientSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.LoadClientSession(context.Background()); err != nil || found {
		t.Fatalf("deleted client session found = %v, error = %v", found, err)
	}
}
