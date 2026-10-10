//go:build server

package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ErrModelCallNotFound 表示平台模型调用记录不存在。
var ErrModelCallNotFound = errors.New("platform AI model call not found")

// ValidationModelCallQueryInvalid 表示平台模型调用记录的筛选或分页条件无效。
const ValidationModelCallQueryInvalid common.FieldCode = "PLATFORM_AI_MODEL_CALL_QUERY_INVALID"

// ModelCallListInput 定义平台模型调用记录的筛选与分页条件：ModelID、Status 为空表示不限，Query 按工作区名称或标识匹配。
type ModelCallListInput struct {
	ModelID  string
	Status   domain.AIModelCallStatus
	Query    string
	Page     int
	PageSize int
}

// ModelCall 定义一次平台模型调用及其归属工作区。
type ModelCall struct {
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
}

// ModelCallAttempt 定义平台模型调用的一次上游尝试。
type ModelCallAttempt struct {
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

// ModelCallDetail 定义平台模型调用及其按顺序排列的上游尝试。
type ModelCallDetail struct {
	ModelCall
	Attempts []ModelCallAttempt
}

// ModelCallList 定义平台模型调用记录分页结果。
type ModelCallList struct {
	Calls []ModelCall
	Page  common.PageInfo
}

// ListModelCalls 按开始时间倒序返回平台模型调用记录分页列表；计数与列表在 db 上分两条语句读取，需要一致快照时传入可重复读事务。
func ListModelCalls(ctx context.Context, db bun.IDB, input ModelCallListInput) (ModelCallList, error) {
	input.Query = strings.TrimSpace(input.Query)
	var valid bool
	input.Page, input.PageSize, valid = common.NormalizePagination(input.Page, input.PageSize)
	input.ModelID, _ = str.NormalizeUUID(input.ModelID)
	if !valid {
		return ModelCallList{}, &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationModelCallQueryInvalid}}
	}
	apply := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.Join("JOIN workspaces AS o ON o.id = amc.workspace_id").
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
	total, err := apply(db.NewSelect().TableExpr("ai_model_calls AS amc")).Count(ctx)
	if err != nil {
		return ModelCallList{}, fmt.Errorf("count platform AI model calls: %w", err)
	}
	calls := make([]ModelCall, 0)
	if err := apply(selectModelCalls(db)).
		OrderExpr("amc.created_at DESC, amc.id DESC").
		Limit(int64(input.PageSize)).
		Offset(int64((input.Page-1)*input.PageSize)).
		Scan(ctx, &calls); err != nil {
		return ModelCallList{}, fmt.Errorf("list platform AI model calls: %w", err)
	}
	return ModelCallList{Calls: calls, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: int(total)}}, nil
}

// GetModelCall 返回平台模型调用及其上游尝试，不存在时返回 ErrModelCallNotFound；需要一致快照时传入可重复读事务。
func GetModelCall(ctx context.Context, db bun.IDB, callID string) (*ModelCallDetail, error) {
	detail := &ModelCallDetail{Attempts: make([]ModelCallAttempt, 0)}
	err := selectModelCalls(db).
		Join("JOIN workspaces AS o ON o.id = amc.workspace_id").
		Where("amc.id = ?", callID).
		Where("amc.model_scope = ?", domain.AIModelScopePlatform).
		Scan(ctx, &detail.ModelCall)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrModelCallNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get platform AI model call: %w", err)
	}
	if err := db.NewSelect().TableExpr("ai_model_call_attempts AS amca").
		ColumnExpr("amca.id::text AS id, amca.created_at, amca.finished_at, amca.provider_name, amca.identifier, amca.status").
		ColumnExpr("amca.input_tokens, amca.cached_input_tokens, amca.output_tokens, amca.error_message").
		Where("amca.call_id = ?", callID).
		OrderExpr("amca.id ASC").
		Scan(ctx, &detail.Attempts); err != nil {
		return nil, fmt.Errorf("list platform AI model call attempts: %w", err)
	}
	return detail, nil
}

// selectModelCalls 返回读取调用记录行的查询，调用方关联 workspaces AS o。
func selectModelCalls(db bun.IDB) *bun.SelectQuery {
	return db.NewSelect().TableExpr("ai_model_calls AS amc").
		ColumnExpr("amc.id::text AS id, amc.created_at, amc.finished_at, amc.model_id::text AS model_id, amc.model_name, amc.model_usage").
		ColumnExpr("o.id::text AS workspace_id, o.name AS workspace_name, amc.actor_type, amc.source_type, amc.status").
		ColumnExpr("amc.input_tokens, amc.cached_input_tokens, amc.output_tokens, amc.error_message").
		ColumnExpr("(SELECT count(*) FROM ai_model_call_attempts AS amca WHERE amca.call_id = amc.id) AS attempt_count")
}
