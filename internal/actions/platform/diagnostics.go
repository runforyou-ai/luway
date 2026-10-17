//go:build server

package platform

import (
	"context"
	"fmt"
	"time"

	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// Diagnostics 定义一次导出中由平台包汇总的诊断信息：平台安装时间、平台概览、运行状态、数据库、全部等待重试与近 7 天失败的任务。
// 授权、部署配置与对象存储检查由调用方在读取部署设置后装配，避免 platform 反向依赖 license / deployment，并保持原有副作用顺序。
type Diagnostics struct {
	GeneratedAt time.Time
	InstalledAt time.Time
	Overview    Overview
	Runtime     RuntimeStatus
	Database    Database
	FailedTasks []servertask.FailedRun
}

// ObjectStorage 定义对象存储检查结果：Enabled 为假表示文件写入服务器本地目录；Error 为存储桶检查失败的原因，可以访问时为空。
type ObjectStorage struct {
	Enabled bool
	Error   string
}

// Database 定义 PostgreSQL 服务端版本与已执行的最新迁移版本。
type Database struct {
	Version   string
	Migration int64
}

// LoadDiagnostics 汇总平台安装时间、平台概览、运行状态、数据库版本、失败任务。
func LoadDiagnostics(ctx context.Context, db *bun.DB, tasks servertask.Monitor) (Diagnostics, error) {
	generatedAt, err := serverstorage.Now(ctx, db)
	if err != nil {
		return Diagnostics{}, err
	}
	diagnostics := Diagnostics{GeneratedAt: generatedAt}
	platform, err := Load(ctx, db)
	if err != nil {
		return Diagnostics{}, err
	}
	diagnostics.InstalledAt = platform.CreatedAt
	if diagnostics.Overview, err = NewOverviewQuery(db).Execute(ctx); err != nil {
		return Diagnostics{}, err
	}
	if diagnostics.Runtime, err = NewRuntimeStatusQuery(db, tasks).Execute(ctx); err != nil {
		return Diagnostics{}, err
	}
	if err := db.NewRaw("SELECT current_setting('server_version')").Scan(ctx, &diagnostics.Database.Version); err != nil {
		return Diagnostics{}, fmt.Errorf("read database version: %w", err)
	}
	if diagnostics.Database.Migration, err = serverstorage.MigrationVersion(ctx, db.DB); err != nil {
		return Diagnostics{}, err
	}
	if diagnostics.FailedTasks, _, err = tasks.FailedRuns(ctx, 0, 0); err != nil {
		return Diagnostics{}, err
	}

	return diagnostics, nil
}
