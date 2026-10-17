//go:build server

// Package servertest 为服务端集成测试提供测试数据库连接配置与空数据库、消息总线与进程内 NATS 服务器、部署状态、任务登记器与测试授权码。
package servertest

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"uuid"

	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	broker "github.com/runforyou-ai/luway/internal/messaging"
	"github.com/runforyou-ai/luway/pkg/clusterbus"
	"github.com/stretchr/testify/require"
)

var sharedMessaging struct {
	sync.Mutex
	connection *broker.Connection
	directory  string
	references int
}

// StartRealtimeBroker 为共享测试数据库中的并行实例提供同一套消息资源。
func StartRealtimeBroker(t *testing.T) *broker.Connection {
	t.Helper()
	sharedMessaging.Lock()
	defer sharedMessaging.Unlock()
	if sharedMessaging.connection == nil {
		directory, err := os.MkdirTemp("", "luway-test-nats-")
		require.NoError(t, err)
		connection, err := broker.Open(context.Background(), serverconfig.Config{Data: serverconfig.DataConfig{Directory: directory}}, uuid.NewV7().String())
		require.NoError(t, err)
		sharedMessaging.connection, sharedMessaging.directory = connection, directory
	}
	sharedMessaging.references++
	t.Cleanup(func() {
		sharedMessaging.Lock()
		defer sharedMessaging.Unlock()
		sharedMessaging.references--
		if sharedMessaging.references == 0 {
			sharedMessaging.connection.Close()
			_ = os.RemoveAll(sharedMessaging.directory)
			sharedMessaging.connection = nil
		}
	})
	return sharedMessaging.connection
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

// StartBus 启动经共享测试 NATS 传输的消息总线，namespace 隔离不同测试的广播，测试结束时关闭。
func StartBus(t *testing.T, namespace, instanceID string) *clusterbus.Bus {
	t.Helper()
	connection := StartRealtimeBroker(t)
	bus := clusterbus.NewNATS(instanceID, clusterbus.NATSOptions{Connection: connection.Conn, Namespace: namespace})
	require.NoError(t, bus.Start())
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// BusNamespace 生成测试独占的总线命名空间。
func BusNamespace() string {
	return "testbus_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:16]
}
