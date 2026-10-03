//go:build server

// Package platformdiagnostics 汇总平台管理员导出的诊断信息。
package platformdiagnostics

import (
	"context"
	"fmt"
	"time"

	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// Diagnostics 定义一次导出的诊断信息：平台概览、运行状态、数据库、全部等待重试与近 7 天失败的任务，以及平台供应商的近期结果。
type Diagnostics struct {
	GeneratedAt time.Time
	Overview    platformaction.Overview
	Runtime     platformaction.RuntimeStatus
	Database    Database
	FailedTasks []servertask.FailedRun
	AIProviders []aiprovideraction.PlatformSummary
}

// Database 定义 PostgreSQL 服务端版本与已执行的最新迁移版本。
type Database struct {
	Version   string
	Migration int64
}

// Query 读取诊断信息。
type Query struct {
	db        *bun.DB
	overview  *platformaction.OverviewQuery
	runtime   *platformaction.RuntimeStatusQuery
	providers *aiprovideraction.ListPlatformQuery
}

// NewQuery 创建诊断信息查询，storage 是部署级对象存储配置。
func NewQuery(db *bun.DB, storage filecontent.S3Config) *Query {
	return &Query{
		db:        db,
		overview:  platformaction.NewOverviewQuery(db),
		runtime:   platformaction.NewRuntimeStatusQuery(db, storage),
		providers: aiprovideraction.NewListPlatformQuery(db),
	}
}

// Execute 汇总平台概览、运行状态、数据库版本、失败任务与平台供应商结果。
func (q *Query) Execute(ctx context.Context) (Diagnostics, error) {
	diagnostics := Diagnostics{GeneratedAt: time.Now()}
	var err error
	if diagnostics.Overview, err = q.overview.Execute(ctx); err != nil {
		return Diagnostics{}, err
	}
	if diagnostics.Runtime, err = q.runtime.Execute(ctx); err != nil {
		return Diagnostics{}, err
	}
	if err := q.db.NewRaw("SELECT current_setting('server_version')").Scan(ctx, &diagnostics.Database.Version); err != nil {
		return Diagnostics{}, fmt.Errorf("read database version: %w", err)
	}
	if diagnostics.Database.Migration, err = serverstorage.MigrationVersion(ctx, q.db.DB); err != nil {
		return Diagnostics{}, err
	}
	if diagnostics.FailedTasks, _, err = servertask.ListFailedRuns(ctx, q.db, 0, 0); err != nil {
		return Diagnostics{}, err
	}
	if diagnostics.AIProviders, err = q.providers.Execute(ctx); err != nil {
		return Diagnostics{}, err
	}
	return diagnostics, nil
}
