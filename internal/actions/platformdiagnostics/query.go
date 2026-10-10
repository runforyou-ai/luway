//go:build server

// Package platformdiagnostics 汇总平台管理员导出的诊断信息。
package platformdiagnostics

import (
	"context"
	"fmt"
	"time"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// bucketCheckTimeout 是导出时检查对象存储桶的时限。
const bucketCheckTimeout = 3 * time.Second

// Diagnostics 定义一次导出的诊断信息：平台安装时间、平台概览、授权与 control 同步结果、部署配置、对象存储检查结果、运行状态、数据库、全部等待重试与近 7 天失败的任务。
type Diagnostics struct {
	GeneratedAt   time.Time
	InstalledAt   time.Time
	Overview      platformaction.Overview
	License       licenseaction.License
	Deployment    deploymentaction.DeploymentSettings
	ObjectStorage ObjectStorage
	Runtime       platformaction.RuntimeStatus
	Database      Database
	FailedTasks   []servertask.FailedRun
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

// Query 读取诊断信息。
type Query struct {
	db         *bun.DB
	overview   *platformaction.OverviewQuery
	license    *licenseaction.LicenseQuery
	deployment *deploymentaction.DeploymentSettingsQuery
	runtime    *platformaction.RuntimeStatusQuery
	tasks      servertask.Monitor
}

// NewQuery 创建诊断信息查询。
func NewQuery(db *bun.DB, tasks servertask.Monitor) *Query {
	return &Query{
		db:         db,
		overview:   platformaction.NewOverviewQuery(db),
		license:    licenseaction.NewLicenseQuery(db),
		deployment: deploymentaction.NewDeploymentSettingsQuery(db),
		runtime:    platformaction.NewRuntimeStatusQuery(db, tasks),
		tasks:      tasks,
	}
}

// Execute 汇总平台安装时间、平台概览、授权、部署配置、对象存储检查结果、运行状态、数据库版本、失败任务；开启对象存储时由执行导出的服务器检查存储桶。
func (q *Query) Execute(ctx context.Context) (Diagnostics, error) {
	generatedAt, err := serverstorage.Now(ctx, q.db)
	if err != nil {
		return Diagnostics{}, err
	}
	diagnostics := Diagnostics{GeneratedAt: generatedAt}
	platform, err := platformaction.Load(ctx, q.db)
	if err != nil {
		return Diagnostics{}, err
	}
	diagnostics.InstalledAt = platform.CreatedAt
	if diagnostics.Overview, err = q.overview.Execute(ctx); err != nil {
		return Diagnostics{}, err
	}
	if diagnostics.License, err = q.license.Execute(ctx); err != nil {
		return Diagnostics{}, err
	}
	if diagnostics.Deployment, err = q.deployment.Execute(ctx); err != nil {
		return Diagnostics{}, err
	}
	diagnostics.ObjectStorage.Enabled = diagnostics.Deployment.S3.Enabled
	if diagnostics.ObjectStorage.Enabled {
		checkCtx, cancel := context.WithTimeout(ctx, bucketCheckTimeout)
		err := filecontent.CheckBucket(checkCtx, diagnostics.Deployment.S3)
		cancel()
		if err != nil {
			diagnostics.ObjectStorage.Error = err.Error()
		}
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
	if diagnostics.FailedTasks, _, err = q.tasks.FailedRuns(ctx, 0, 0); err != nil {
		return Diagnostics{}, err
	}

	return diagnostics, nil
}
