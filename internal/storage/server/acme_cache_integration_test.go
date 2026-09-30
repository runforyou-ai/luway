//go:build server

package server

import (
	"bytes"
	"context"
	"errors"
	"testing"

	servertest "github.com/runforyou-ai/cervi/internal/servertest"
	"golang.org/x/crypto/acme/autocert"
)

// TestACMECacheWithPostgreSQL 验证自动 TLS 缓存可以跨实例读写和删除。
func TestACMECacheWithPostgreSQL(t *testing.T) {
	store, err := Open(context.Background(), servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cache := NewACMECache(store.DB())
	const key = "acme-cache-integration-test"
	if err := cache.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(t.Context(), key); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("missing cache error = %v, want ErrCacheMiss", err)
	}
	if err := cache.Put(t.Context(), key, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(t.Context(), key, []byte("second")); err != nil {
		t.Fatal(err)
	}
	data, err := NewACMECache(store.DB()).Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("second")) {
		t.Fatalf("cache data = %q, want second", data)
	}
	if err := cache.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(t.Context(), key); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("deleted cache error = %v, want ErrCacheMiss", err)
	}
}
