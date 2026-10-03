//go:build server

package platformmodel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// CallListInput 定义平台模型调用记录的筛选与分页条件：ModelID、Status 为空表示不限，Query 按工作区名称或标识匹配。
type CallListInput struct {
	ModelID  string
	Status   domain.AIModelCallStatus
	Query    string
	Page     int
	PageSize int
}

// Call 定义一次平台模型调用及其归属工作区。
type Call struct {
	ID                string                   `bun:"id"`
	CreatedAt         time.Time                `bun:"created_at"`
	FinishedAt        *time.Time               `bun:"finished_at"`
	ModelID           string                   `bun:"model_id"`
	ModelName         string                   `bun:"model_name"`
	Usage             domain.AIModelUsage      `bun:"model_usage"`
	WorkspaceID       string                   `bun:"workspace_id"`
	WorkspaceName     string                   `bun:"workspace_name"`
	Actor             domain.AIModelCallActor  `bun:"actor_type"`
	Source            domain.AIModelCallSource `bun:"source_type"`
	Status            domain.AIModelCallStatus `bun:"status"`
	InputTokens       int64                    `bun:"input_tokens"`
	CachedInputTokens int64                    `bun:"cached_input_tokens"`
	OutputTokens      int64                    `bun:"output_tokens"`
	ErrorMessage      string                   `bun:"error_message"`
	AttemptCount      int                      `bun:"attempt_count"`
	Credits           int64                    `bun:"credits"`
	CreditShortfall   int64                    `bun:"credit_shortfall"`
}

// CallAttempt 定义平台模型调用的一次上游尝试。
type CallAttempt struct {
	ID                string                   `bun:"id"`
	CreatedAt         time.Time                `bun:"created_at"`
	FinishedAt        *time.Time               `bun:"finished_at"`
	ProviderName      string                   `bun:"provider_name"`
	Identifier        string                   `bun:"identifier"`
	Status            domain.AIModelCallStatus `bun:"status"`
	InputTokens       int64                    `bun:"input_tokens"`
	CachedInputTokens int64                    `bun:"cached_input_tokens"`
	OutputTokens      int64                    `bun:"output_tokens"`
	ErrorMessage      string                   `bun:"error_message"`
}

// CallDetail 定义平台模型调用及其按顺序排列的上游尝试。
type CallDetail struct {
	Call
	Attempts []CallAttempt
}

// CallList 定义平台模型调用记录分页结果。
type CallList struct {
	Calls []Call
	Page  common.PageInfo
}

// ListCallsQuery 读取平台模型调用记录。
type ListCallsQuery struct{ db *bun.DB }

// NewListCallsQuery 创建平台模型调用记录查询。
func NewListCallsQuery(db *bun.DB) *ListCallsQuery {
	return &ListCallsQuery{db: db}
}

// Execute 按开始时间倒序返回平台模型调用记录分页列表。
func (q *ListCallsQuery) Execute(ctx context.Context, input CallListInput) (CallList, error) {
	input.Query = strings.TrimSpace(input.Query)
	var valid bool
	input.Page, input.PageSize, valid = common.NormalizePagination(input.Page, input.PageSize)
	if input.ModelID != "" && !common.ValidUUID(input.ModelID) {
		valid = false
	}
	switch input.Status {
	case "", domain.AIModelCallStatusRunning, domain.AIModelCallStatusSucceeded, domain.AIModelCallStatusFailed,
		domain.AIModelCallStatusCanceled, domain.AIModelCallStatusTimedOut:
	default:
		valid = false
	}
	if !valid {
		return CallList{}, &common.FieldError{Fields: map[string]ValidationCode{"query": ValidationCallQueryInvalid}}
	}
	apply := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.Join("JOIN organizations AS o ON o.id = amc.organization_id").
			Where("amc.model_scope = ?", domain.AIModelScopePlatform)
		if input.ModelID != "" {
			query = query.Where("amc.model_id = ?", input.ModelID)
		}
		if input.Status != "" {
			query = query.Where("amc.status = ?", input.Status)
		}
		if input.Query != "" {
			pattern := common.ContainsPattern(input.Query)
			query = query.Where("(o.name ILIKE ? OR o.slug ILIKE ?)", pattern, pattern)
		}
		return query
	}
	total, err := apply(q.db.NewSelect().TableExpr("ai_model_calls AS amc")).Count(ctx)
	if err != nil {
		return CallList{}, fmt.Errorf("count platform AI model calls: %w", err)
	}
	calls := make([]Call, 0)
	if err := apply(selectCalls(q.db)).
		OrderExpr("amc.created_at DESC, amc.id DESC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &calls); err != nil {
		return CallList{}, fmt.Errorf("list platform AI model calls: %w", err)
	}
	return CallList{Calls: calls, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}

// GetCallQuery 读取平台模型调用详情。
type GetCallQuery struct{ db *bun.DB }

// NewGetCallQuery 创建平台模型调用详情查询。
func NewGetCallQuery(db *bun.DB) *GetCallQuery {
	return &GetCallQuery{db: db}
}

// Execute 返回平台模型调用及其上游尝试，不存在时返回 ErrNotFound。
func (q *GetCallQuery) Execute(ctx context.Context, callID string) (*CallDetail, error) {
	if !common.ValidUUID(callID) {
		return nil, ErrNotFound
	}
	detail := &CallDetail{Attempts: make([]CallAttempt, 0)}
	err := selectCalls(q.db).
		Join("JOIN organizations AS o ON o.id = amc.organization_id").
		Where("amc.id = ?", callID).
		Where("amc.model_scope = ?", domain.AIModelScopePlatform).
		Scan(ctx, &detail.Call)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get platform AI model call: %w", err)
	}
	if err := q.db.NewSelect().TableExpr("ai_model_call_attempts AS amca").
		ColumnExpr("amca.id::text AS id, amca.created_at, amca.finished_at, amca.provider_name, amca.identifier, amca.status").
		ColumnExpr("amca.input_tokens, amca.cached_input_tokens, amca.output_tokens, amca.error_message").
		Where("amca.call_id = ?", callID).
		OrderExpr("amca.id ASC").
		Scan(ctx, &detail.Attempts); err != nil {
		return nil, fmt.Errorf("list platform AI model call attempts: %w", err)
	}
	return detail, nil
}

// selectCalls 返回读取调用记录行的查询，调用方关联 organizations AS o。
func selectCalls(db bun.IDB) *bun.SelectQuery {
	return db.NewSelect().TableExpr("ai_model_calls AS amc").
		ColumnExpr("amc.id::text AS id, amc.created_at, amc.finished_at, amc.model_id::text AS model_id, amc.model_name, amc.model_usage").
		ColumnExpr("o.id::text AS workspace_id, o.name AS workspace_name, amc.actor_type, amc.source_type, amc.status").
		ColumnExpr("amc.input_tokens, amc.cached_input_tokens, amc.output_tokens, amc.error_message, amc.credits, amc.credit_shortfall").
		ColumnExpr("(SELECT count(*) FROM ai_model_call_attempts AS amca WHERE amca.call_id = amc.id) AS attempt_count")
}
