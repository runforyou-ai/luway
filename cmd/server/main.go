//go:build server

// 服务端程序入口。
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/runforyou-ai/luway/frontend"
	"github.com/runforyou-ai/luway/internal/serverapp"
	"github.com/runforyou-ai/luway/internal/servercli"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
)

// main 执行服务端程序的命令并记录无法恢复的运行错误。
func main() {
	if err := servercli.Run(os.Args[1:], serverapp.Edition{Migrations: serverstorage.CoreMigrations(), WebAssets: frontend.Dist()}.Program()); err != nil {
		slog.ErrorContext(context.Background(), "运行失败", "error", err)
		os.Exit(1)
	}
}
