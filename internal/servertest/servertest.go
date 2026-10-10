//go:build server

// Package servertest 为服务端集成测试提供测试数据库连接配置与空数据库、消息总线与进程内 NATS 服务器、部署状态、任务登记器与测试授权码。
package servertest

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"uuid"

	natstest "github.com/nats-io/nats-server/v2/test"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/pkg/clusterbus"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// StartRealtimeBroker 以默认命名空间启动与生产一致的内嵌 APP、CLIENT、SYS 账户与本机 WebSocket，测试结束时关闭。
func StartRealtimeBroker(t *testing.T) *broker.Connection {
	t.Helper()
	return StartNamespaceBroker(t, "app")
}

// StartNamespaceBroker 以指定命名空间启动内嵌实时 NATS，测试结束时关闭。
func StartNamespaceBroker(t *testing.T, namespace string) *broker.Connection {
	t.Helper()
	connection, err := broker.Open(serverconfig.Config{Data: serverconfig.DataConfig{Directory: t.TempDir()}, NATS: serverconfig.NATSConfig{Namespace: namespace}}, uuid.NewV7().String())
	require.NoError(t, err)
	t.Cleanup(connection.Close)
	return connection
}

// DatabaseConfig 从测试专用的 PostgreSQL 分项环境变量读取连接配置；未设置 TEST_POSTGRES_HOST 时跳过当前测试，数据库名必须以 _test 结尾。
func DatabaseConfig(t *testing.T) serverconfig.DatabaseConfig {
	t.Helper()
	host := os.Getenv("TEST_POSTGRES_HOST")
	if host == "" {
		t.Skip("TEST_POSTGRES_HOST is not set")
	}
	port, err := strconv.Atoi(os.Getenv("TEST_POSTGRES_PORT"))
	require.NoError(t, err, "TEST_POSTGRES_PORT is invalid")
	databaseName := os.Getenv("TEST_POSTGRES_DB")
	require.True(t, strings.HasSuffix(databaseName, "_test"), "TEST_POSTGRES_DB must end with _test")
	return serverconfig.DatabaseConfig{
		Host:     host,
		Port:     port,
		User:     os.Getenv("TEST_POSTGRES_USER"),
		Password: os.Getenv("TEST_POSTGRES_PASSWORD"),
		Name:     databaseName,
		SSLMode:  os.Getenv("TEST_POSTGRES_SSLMODE"),
	}
}

// StartBus 启动使用测试库 LISTEN/NOTIFY 的消息总线，channel 隔离同一测试库中不同测试的广播，测试结束时关闭。
func StartBus(t *testing.T, db *bun.DB, channel, instanceID string) *clusterbus.Bus {
	t.Helper()
	listenConfig, err := serverstorage.ConnConfig(DatabaseConfig(t), "")
	require.NoError(t, err)
	bus := clusterbus.NewPostgres(instanceID, db.DB, listenConfig, clusterbus.PostgresOptions{Channel: channel})
	require.NoError(t, bus.Start())
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// BusChannel 生成测试独占的总线广播频道名。
func BusChannel() string {
	return "test_bus_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:16]
}

// StartNATSServer 启动进程内 NATS 服务器并返回其连接地址，测试结束时关闭。
func StartNATSServer(t *testing.T) string {
	t.Helper()
	server := natstest.RunRandClientPortServer()
	t.Cleanup(server.Shutdown)
	return server.ClientURL()
}

// StartNATSBus 启动连接 url 的消息总线，测试结束时关闭。
func StartNATSBus(t *testing.T, url, instanceID string) *clusterbus.Bus {
	t.Helper()
	bus := clusterbus.NewNATS(instanceID, clusterbus.NATSOptions{URL: url, Namespace: "app"})
	require.NoError(t, bus.Start())
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}
