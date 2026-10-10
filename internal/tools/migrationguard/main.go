//go:build server

// migrationguard 在执行服务端迁移命令前校验数据库中已执行的迁移版本都有迁移源文件。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
)

// main 按 libpq 环境变量连接 PostgreSQL 并执行迁移源校验，连接或校验失败时以非零状态退出。
func main() {
	config, err := pgx.ParseConfig("")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	db := stdlib.OpenDB(*config)
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "无法连接数据库：", err)
		os.Exit(2)
	}
	if err := serverstorage.CoreMigrations().Verify(context.Background(), db); err != nil {
		fmt.Fprintln(os.Stderr, "数据库迁移记录与源文件不一致，请执行 wails3 task db:reset 重建数据库：", err)
		os.Exit(1)
	}
}
