//go:build server

package server

import (
	"testing"

	serverconfig "github.com/runforyou-ai/cervi/internal/config/server"
)

// TestPostgresDSN 验证驱动连接地址会正确编码分项配置。
func TestPostgresDSN(t *testing.T) {
	config := serverconfig.DatabaseConfig{
		Host:     "2001:db8::1",
		Port:     5432,
		User:     "app",
		Password: "secret@value",
		Name:     "main",
		SSLMode:  "require",
	}
	const expected = "postgres://app:secret%40value@[2001:db8::1]:5432/main?sslmode=require"
	if actual := postgresDSN(config); actual != expected {
		t.Fatalf("PostgreSQL 驱动连接地址 = %q，期望 %q", actual, expected)
	}
}
