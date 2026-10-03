//go:build server

package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// bucketCheckTimeout 是检查对象存储桶的时限。
const bucketCheckTimeout = 3 * time.Second

// RuntimeStatus 定义服务端进程、外部依赖与后台任务各队列的运行状态。
type RuntimeStatus struct {
	Servers       []ServerInstance
	ObjectStorage ObjectStorageStatus
	Control       ControlStatus
	Queues        []servertask.QueueStatus
}

// ObjectStorageStatus 定义对象存储状态：Enabled 为假表示文件写入服务器本地目录；Error 为存储桶检查失败的原因，可以访问时为空。
type ObjectStorageStatus struct {
	Enabled bool
	Error   string
}

// ControlStatus 定义与 control 同步的结果：SyncedAt 为最近一次成功的时间，FailedAt 与 Error 为此后最近一次失败的时间与原因。
type ControlStatus struct {
	SyncedAt *time.Time
	FailedAt *time.Time
	Error    string
}

// RuntimeStatusQuery 读取平台运行状态。
type RuntimeStatusQuery struct {
	db      *bun.DB
	storage filecontent.S3Config
}

// NewRuntimeStatusQuery 创建运行状态查询，storage 是部署级对象存储配置。
func NewRuntimeStatusQuery(db *bun.DB, storage filecontent.S3Config) *RuntimeStatusQuery {
	return &RuntimeStatusQuery{db: db, storage: storage}
}

// Execute 返回服务端进程、对象存储与 control 状态，以及有未结束任务或近期失败任务的各队列运行概况；开启对象存储时实时检查存储桶。
func (q *RuntimeStatusQuery) Execute(ctx context.Context) (RuntimeStatus, error) {
	platform, err := Load(ctx, q.db)
	if err != nil {
		return RuntimeStatus{}, err
	}
	status := RuntimeStatus{
		ObjectStorage: ObjectStorageStatus{Enabled: q.storage.Enabled},
		Control:       ControlStatus{SyncedAt: platform.ControlSyncedAt, FailedAt: platform.ControlFailedAt, Error: platform.ControlError},
	}
	if status.Servers, err = listInstances(ctx, q.db); err != nil {
		return RuntimeStatus{}, err
	}
	if status.Queues, err = servertask.ReadQueueStatuses(ctx, q.db); err != nil {
		return RuntimeStatus{}, err
	}
	if q.storage.Enabled {
		checkCtx, cancel := context.WithTimeout(ctx, bucketCheckTimeout)
		defer cancel()
		if err := filecontent.CheckBucket(checkCtx, q.storage); err != nil {
			status.ObjectStorage.Error = err.Error()
		}
	}
	return status, nil
}

// FailedTask 定义一次失败或等待重试的后台任务运行；WorkspaceName 为所属工作区名称，平台级任务为空。
type FailedTask struct {
	servertask.FailedRun
	WorkspaceName *string
}

// FailedTaskListInput 定义失败任务列表的分页。
type FailedTaskListInput struct {
	Page     int
	PageSize int
}

// FailedTaskListOutput 定义失败任务分页结果。
type FailedTaskListOutput struct {
	Tasks []FailedTask
	Page  common.PageInfo
}

// FailedTaskListQuery 读取近期失败与等待重试的后台任务。
type FailedTaskListQuery struct {
	db *bun.DB
}

// NewFailedTaskListQuery 创建失败任务列表查询。
func NewFailedTaskListQuery(db *bun.DB) *FailedTaskListQuery {
	return &FailedTaskListQuery{db: db}
}

// Execute 按最近一次失败时间倒序返回一页失败与等待重试的后台任务及所属工作区名称。
func (q *FailedTaskListQuery) Execute(ctx context.Context, input FailedTaskListInput) (FailedTaskListOutput, error) {
	page, pageSize, valid := common.NormalizePagination(input.Page, input.PageSize)
	if !valid {
		return FailedTaskListOutput{}, &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationQueryInvalid}}
	}
	runs, total, err := servertask.ListFailedRuns(ctx, q.db, pageSize, (page-1)*pageSize)
	if err != nil {
		return FailedTaskListOutput{}, err
	}
	// 一次读取本页涉及的工作区名称。
	names := map[string]string{}
	organizationIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		if run.OrganizationID != nil {
			organizationIDs = append(organizationIDs, *run.OrganizationID)
		}
	}
	if len(organizationIDs) > 0 {
		var workspaces []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := q.db.NewSelect().TableExpr("organizations").ColumnExpr("id::text AS id, name").
			Where("id IN (?)", bun.In(organizationIDs)).Scan(ctx, &workspaces); err != nil {
			return FailedTaskListOutput{}, fmt.Errorf("read failed task workspaces: %w", err)
		}
		for _, workspace := range workspaces {
			names[workspace.ID] = workspace.Name
		}
	}
	tasks := make([]FailedTask, 0, len(runs))
	for _, run := range runs {
		task := FailedTask{FailedRun: run}
		if run.OrganizationID != nil {
			if name, ok := names[*run.OrganizationID]; ok {
				task.WorkspaceName = &name
			}
		}
		tasks = append(tasks, task)
	}
	return FailedTaskListOutput{Tasks: tasks, Page: common.PageInfo{Number: page, Size: pageSize, Total: total}}, nil
}
