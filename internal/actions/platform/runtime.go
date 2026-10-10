//go:build server

package platform

import (
	"context"
	"fmt"

	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/common"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// RuntimeStatus 定义服务端进程与后台任务各队列的运行状态；DelayedTasks 为推迟执行或等待重试的任务数，NotifyQueueUsage 为 PostgreSQL 通知队列的使用比例（0 到 1）。
type RuntimeStatus struct {
	Servers          []serverinstanceaction.ServerInstance
	Queues           []servertask.QueueStatus
	DelayedTasks     int
	NotifyQueueUsage float64
}

// RuntimeStatusQuery 读取平台运行状态。
type RuntimeStatusQuery struct {
	db    *bun.DB
	tasks servertask.Monitor
}

// NewRuntimeStatusQuery 创建运行状态查询。
func NewRuntimeStatusQuery(db *bun.DB, tasks servertask.Monitor) *RuntimeStatusQuery {
	return &RuntimeStatusQuery{db: db, tasks: tasks}
}

// Execute 返回服务端进程、各队列的任务概况、推迟执行或等待重试的任务数，以及 PostgreSQL 通知队列的使用比例。
func (q *RuntimeStatusQuery) Execute(ctx context.Context) (RuntimeStatus, error) {
	var status RuntimeStatus
	var err error
	if status.Servers, err = serverinstanceaction.ListInstances(ctx, q.db); err != nil {
		return RuntimeStatus{}, err
	}
	tasks, err := q.tasks.TaskStatus(ctx)
	if err != nil {
		return RuntimeStatus{}, err
	}
	status.Queues, status.DelayedTasks = tasks.Queues, tasks.Delayed
	if err := q.db.NewRaw("SELECT pg_notification_queue_usage()").Scan(ctx, &status.NotifyQueueUsage); err != nil {
		return RuntimeStatus{}, fmt.Errorf("read notification queue usage: %w", err)
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
	db    *bun.DB
	tasks servertask.Monitor
}

// NewFailedTaskListQuery 创建失败任务列表查询。
func NewFailedTaskListQuery(db *bun.DB, tasks servertask.Monitor) *FailedTaskListQuery {
	return &FailedTaskListQuery{db: db, tasks: tasks}
}

// Execute 按最近一次失败时间倒序返回一页失败与等待重试的后台任务及所属工作区名称。
func (q *FailedTaskListQuery) Execute(ctx context.Context, input FailedTaskListInput) (FailedTaskListOutput, error) {
	page, pageSize, valid := common.NormalizePagination(input.Page, input.PageSize)
	if !valid {
		return FailedTaskListOutput{}, &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationQueryInvalid}}
	}
	runs, total, err := q.tasks.FailedRuns(ctx, pageSize, (page-1)*pageSize)
	if err != nil {
		return FailedTaskListOutput{}, err
	}
	// 一次读取本页涉及的工作区名称。
	names := map[string]string{}
	workspaceIDs := arr.FilterMap(runs, func(run servertask.FailedRun) (string, bool) {
		return support.Deref(run.WorkspaceID), run.WorkspaceID != nil
	})
	if len(workspaceIDs) > 0 {
		var workspaces []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := q.db.NewSelect().TableExpr("workspaces").ColumnExpr("id::text AS id, name").
			Where("id IN (?)", bun.List(workspaceIDs)).Scan(ctx, &workspaces); err != nil {
			return FailedTaskListOutput{}, fmt.Errorf("read failed task workspaces: %w", err)
		}
		for _, workspace := range workspaces {
			names[workspace.ID] = workspace.Name
		}
	}
	tasks := make([]FailedTask, 0, len(runs))
	for _, run := range runs {
		task := FailedTask{FailedRun: run}
		if run.WorkspaceID != nil {
			if name, ok := names[*run.WorkspaceID]; ok {
				task.WorkspaceName = &name
			}
		}
		tasks = append(tasks, task)
	}
	return FailedTaskListOutput{Tasks: tasks, Page: common.PageInfo{Number: page, Size: pageSize, Total: total}}, nil
}
