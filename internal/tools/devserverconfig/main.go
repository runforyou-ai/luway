//go:build server

// devserverconfig 按当前 worktree 的 .env 重写开发服务端的配置文件：直接运行时数据目录为工作目录下的 data；加 -container 时生成容器使用的配置，监听全部地址、经 host.docker.internal 连接数据库并使用镜像的数据目录。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
)

// main 删除原配置文件后写入 .env 中已设置的启动配置。
func main() {
	container := flag.Bool("container", false, "生成容器使用的配置")
	flag.Parse()
	if err := os.Remove(serverconfig.Path()); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var settings []serverconfig.Setting
	if !*container {
		data, err := filepath.Abs("data")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		settings = append(settings, serverconfig.Setting{Key: "data.directory", Value: data})
	}
	// 按配置项顺序读取 .env 中对应的变量，未设置的配置项使用默认值。
	for _, item := range [][2]string{
		{"server.host", "SERVER_HOST"}, {"server.port", "SERVER_PORT"}, {"server.httpsPort", "SERVER_HTTPS_PORT"},
		{"database.host", "POSTGRES_HOST"}, {"database.port", "POSTGRES_PORT"}, {"database.user", "POSTGRES_USER"},
		{"database.password", "POSTGRES_PASSWORD"}, {"database.name", "POSTGRES_DB"}, {"database.sslMode", "POSTGRES_SSLMODE"},
		{"server.clientIPHeader", "CLIENT_IP_HEADER"}, {"server.countryHeader", "COUNTRY_HEADER"}, {"server.egressIP", "EGRESS_IP"},
		{"nats.url", "NATS_URL"}, {"nats.namespace", "NATS_NAMESPACE"}, {"log.level", "LOG_LEVEL"},
		{"nats.calloutSeed", "NATS_CALLOUT_SEED"}, {"nats.webSocketURL", "NATS_WEBSOCKET_URL"}, {"nats.systemURL", "NATS_SYSTEM_URL"}, {"nats.replicas", "NATS_REPLICAS"},
	} {
		if value := os.Getenv(item[1]); value != "" {
			settings = append(settings, serverconfig.Setting{Key: item[0], Value: value})
		}
	}
	if *container {
		settings = append(settings, serverconfig.Setting{Key: "server.host", Value: "0.0.0.0"}, serverconfig.Setting{Key: "database.host", Value: "host.docker.internal"})
	}
	if err := serverconfig.SetValues(settings); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
