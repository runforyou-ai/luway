//go:build server

package platform

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/common"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// TaskQueuesQuery 读取平台后台任务各队列的运行概况。
type TaskQueuesQuery struct {
	db *bun.DB
}

// NewTaskQueuesQuery 创建后台任务队列概况查询。
func NewTaskQueuesQuery(db *bun.DB) *TaskQueuesQuery {
	return &TaskQueuesQuery{db: db}
}

// Execute 返回有未结束任务或近期失败任务的各队列运行概况。
func (q *TaskQueuesQuery) Execute(ctx context.Context) ([]servertask.QueueStatus, error) {
	return servertask.ReadQueueStatuses(ctx, q.db)
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
