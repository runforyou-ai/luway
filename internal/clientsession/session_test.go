package clientsession

import (
	"context"
	"testing"
	"time"
)

type memorySessionStore struct {
	credential Credential
	found      bool
	saves      int
	deletes    int
}

// LoadClientSession 返回测试中保存的登录凭据。
func (s *memorySessionStore) LoadClientSession(context.Context) (Credential, bool, error) {
	return s.credential, s.found, nil
}

// SaveClientSession 保存测试登录凭据。
func (s *memorySessionStore) SaveClientSession(_ context.Context, credential Credential) error {
	s.credential = credential
	s.found = true
	s.saves++
	return nil
}

// DeleteClientSession 删除测试登录凭据。
func (s *memorySessionStore) DeleteClientSession(context.Context) error {
	s.credential = Credential{}
	s.found = false
	s.deletes++
	return nil
}

// TestManagerRestoresScopesAndClearsCredential 验证会话管理器恢复、限定并清理登录凭据。
func TestManagerRestoresScopesAndClearsCredential(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	store := &memorySessionStore{
		credential: Credential{
			ServerURL: "https://app.example.com",
			Token:     "token-1",
			ExpiresAt: expiresAt,
		},
		found: true,
	}
	manager, err := NewManager(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	credential, found := manager.Current(context.Background(), "https://app.example.com")
	if !found || credential.Token != "token-1" || !credential.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("credential = %#v, found = %v", credential, found)
	}
	if _, found := manager.Current(context.Background(), "https://other.example.com"); found {
		t.Fatal("other server credential was returned")
	}
	if err := manager.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.found || store.deletes != 1 {
		t.Fatalf("store found = %v, deletes = %d", store.found, store.deletes)
	}
}

// TestManagerEstablishesCredential 验证会话管理器持久化并启用新的登录凭据。
func TestManagerEstablishesCredential(t *testing.T) {
	store := &memorySessionStore{}
	manager, err := NewManager(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{
		ServerURL: "https://app.example.com",
		Token:     "token-1",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := manager.Establish(context.Background(), credential); err != nil {
		t.Fatal(err)
	}
	if store.saves != 1 || store.credential.ServerURL != "https://app.example.com" {
		t.Fatalf("saved credential = %#v, saves = %d", store.credential, store.saves)
	}
	rejected := store.credential
	replacement := rejected
	replacement.Token = "token-2"
	if err := manager.Establish(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	if err := manager.ClearIfCurrent(context.Background(), rejected); err != nil {
		t.Fatal(err)
	}
	if !store.found || store.credential.Token != "token-2" {
		t.Fatalf("stale credential cleared saved session: %#v", store.credential)
	}
	if err := manager.ClearIfCurrent(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	if store.found {
		t.Fatal("rejected current credential was not cleared")
	}
}

// TestManagerDeletesExpiredCredential 验证启动时删除已过期的登录凭据。
func TestManagerDeletesExpiredCredential(t *testing.T) {
	store := &memorySessionStore{
		credential: Credential{
			ServerURL: "https://app.example.com",
			Token:     "expired-token",
			ExpiresAt: time.Now().Add(-time.Minute),
		},
		found: true,
	}
	manager, err := NewManager(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := manager.Current(context.Background(), "https://app.example.com"); found {
		t.Fatal("expired credential was returned")
	}
	if store.found || store.deletes != 1 {
		t.Fatalf("store found = %v, deletes = %d", store.found, store.deletes)
	}
}
