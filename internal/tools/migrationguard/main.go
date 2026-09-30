//go:build server

// migrationguard 在执行服务端迁移命令前校验数据库中已执行的迁移版本都有迁移源文件。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	serverstorage "github.com/runforyou-ai/cervi/internal/storage/server"
	"github.com/uptrace/bun/driver/pgdriver"
)

// main 连接参数给出的 PostgreSQL 地址并执行迁移源校验，校验失败时以非零状态退出。
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: migrationguard <postgres-url>")
		os.Exit(2)
	}
	db := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(os.Args[1])))
	defer db.Close()
	if err := serverstorage.VerifyMigrationSources(context.Background(), db); err != nil {
		fmt.Fprintln(os.Stderr, "数据库迁移记录与源文件不一致，请删除数据库后重新执行 migrate：", err)
		os.Exit(1)
	}
}
