//go:build server

package platform

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"uuid"

	serverlogaction "github.com/runforyou-ai/luway/internal/actions/serverlog"
	"github.com/runforyou-ai/luway/internal/common"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// serverLogMaxPageSize 是日志列表单页的最大条数。
const serverLogMaxPageSize = 200

// ServerLogListInput 定义服务端日志列表的游标、页大小与筛选条件：MinLevel 为空时包含全部级别，Entry 匹配业务入口方法或后台任务名称，其余条件为空时不筛选。
type ServerLogListInput struct {
	Cursor      string
	PageSize    int
	MinLevel    *int
	InstanceID  string
	WorkspaceID string
	Entry       string
	TraceID     string
}

// ServerLogRecord 是一条服务端日志及其所属工作区名称。
type ServerLogRecord struct {
	servermodels.ServerLog `bun:",extend"`
	WorkspaceName          *string `bun:"workspace_name"`
}

// ServerLogListOutput 定义一页服务端日志；NextCursor 为空表示没有更早的日志。
type ServerLogListOutput struct {
	Logs       []ServerLogRecord
	NextCursor string
}

// ServerLogListQuery 读取保留期内的服务端日志。
type ServerLogListQuery struct {
	db *bun.DB
}

// NewServerLogListQuery 创建服务端日志列表查询。
func NewServerLogListQuery(db *bun.DB) *ServerLogListQuery {
	return &ServerLogListQuery{db: db}
}

// Execute 按记录时间倒序返回游标之前的一页日志。
func (q *ServerLogListQuery) Execute(ctx context.Context, input ServerLogListInput) (ServerLogListOutput, error) {
	invalid := &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationQueryInvalid}}
	pageSize := input.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > serverLogMaxPageSize {
		return ServerLogListOutput{}, invalid
	}
	records := make([]ServerLogRecord, 0, pageSize+1)
	query := q.db.NewSelect().Model(&records).
		ColumnExpr("sl.*").ColumnExpr("o.name AS workspace_name").
		Join("LEFT JOIN workspaces AS o ON o.id::text = sl.workspace_id").
		Where("sl.occurred_at >= now() - make_interval(days => ?)", serverlogaction.ServerLogRetentionDays)
	if input.Cursor != "" {
		// 游标由上一页最后一条日志的记录时间与编号组成。
		decoded, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		occurredText, id, found := strings.Cut(string(decoded), "|")
		occurredAt, parseErr := time.Parse(time.RFC3339Nano, occurredText)
		if err != nil || !found || parseErr != nil || !validUUID(id) {
			return ServerLogListOutput{}, invalid
		}
		query = query.Where("(sl.occurred_at, sl.id) < (?, ?)", occurredAt, id)
	}
	if input.MinLevel != nil {
		query = query.Where("sl.level >= ?", *input.MinLevel)
	}
	if input.InstanceID != "" {
		if !validUUID(input.InstanceID) {
			return ServerLogListOutput{}, invalid
		}
		query = query.Where("sl.instance_id = ?", input.InstanceID)
	}
	if input.WorkspaceID != "" {
		query = query.Where("sl.workspace_id = ?", input.WorkspaceID)
	}
	if input.Entry != "" {
		query = query.WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
			return query.Where("sl.operation = ?", input.Entry).WhereOr("sl.action = ?", input.Entry)
		})
	}
	if input.TraceID != "" {
		query = query.Where("sl.trace_id = ?", input.TraceID)
	}
	if err := query.OrderExpr("sl.occurred_at DESC, sl.id DESC").Limit(int64(pageSize + 1)).Scan(ctx); err != nil {
		return ServerLogListOutput{}, fmt.Errorf("list server logs: %w", err)
	}
	output := ServerLogListOutput{Logs: records}
	if len(records) > pageSize {
		output.Logs = records[:pageSize]
		last := output.Logs[pageSize-1]
		output.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last.OccurredAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID))
	}
	return output, nil
}

// validUUID 判断文本是否为规范的小写 UUID。
func validUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
