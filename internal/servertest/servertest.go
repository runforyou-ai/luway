//go:build server

// Package servertest 为服务端集成测试提供测试数据库连接配置。
package servertest

import (
	"os"
	"strconv"
	"strings"
	"testing"

	serverconfig "github.com/runforyou-ai/cervi/internal/config/server"
)

// DatabaseConfig 从测试专用的 PostgreSQL 分项环境变量读取连接配置。
//
// 未设置 TEST_POSTGRES_HOST 时跳过当前测试；数据库名必须以 _test 结尾，
// 使集成测试只连接测试库。
func DatabaseConfig(t *testing.T) serverconfig.DatabaseConfig {
	t.Helper()
	host := os.Getenv("TEST_POSTGRES_HOST")
	if host == "" {
		t.Skip("TEST_POSTGRES_HOST is not set")
	}
	port, err := strconv.Atoi(os.Getenv("TEST_POSTGRES_PORT"))
	if err != nil {
		t.Fatalf("TEST_POSTGRES_PORT is invalid: %v", err)
	}
	databaseName := os.Getenv("TEST_POSTGRES_DB")
	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatalf("TEST_POSTGRES_DB must end with _test")
	}
	return serverconfig.DatabaseConfig{
		Host:     host,
		Port:     port,
		User:     os.Getenv("TEST_POSTGRES_USER"),
		Password: os.Getenv("TEST_POSTGRES_PASSWORD"),
		Name:     databaseName,
		SSLMode:  os.Getenv("TEST_POSTGRES_SSLMODE"),
	}
}

// NATSConfig 从 TEST_NATS_URL 读取 NATS 地址并使用独立命名空间；未设置时跳过当前测试。
func NATSConfig(t *testing.T, namespace string) serverconfig.NATSConfig {
	t.Helper()
	url := os.Getenv("TEST_NATS_URL")
	if url == "" {
		t.Skip("TEST_NATS_URL is not set")
	}
	return serverconfig.NATSConfig{URL: url, Namespace: namespace}
}
