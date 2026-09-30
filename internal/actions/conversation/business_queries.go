//go:build server

package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// BusinessQuery 表示 AI 客服在一次运行中调用业务查询工具的记录。
type BusinessQuery struct {
	ID       string
	CalledAt time.Time
	ToolCall agentruntime.ToolCall
}

// ListBusinessQueriesQuery 读取客户会话当前客服周期内 AI 客服查询业务系统的记录。
type ListBusinessQueriesQuery struct{ db *bun.DB }

// NewListBusinessQueriesQuery 创建业务查询记录查询。
func NewListBusinessQueriesQuery(db *bun.DB) *ListBusinessQueriesQuery {
	return &ListBusinessQueriesQuery{db: db}
}

// Execute 按调用时间倒序返回当前周期已进入终态的 AI 客服运行中的远程工具调用。
func (q *ListBusinessQueriesQuery) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) ([]BusinessQuery, error) {
	if !common.ValidUUID(conversationID) {
		return nil, ErrConversationNotFound
	}
	queries := make([]BusinessQuery, 0)
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := AuthorizeConversationHistory(ctx, tx, identity, conversationID); err != nil {
			return err
		}
		rows := make([]struct {
			ID          string          `bun:"id"`
			CompletedAt time.Time       `bun:"completed_at"`
			Payload     json.RawMessage `bun:"payload"`
		}, 0)
		if err := tx.NewSelect().
			TableExpr("service_conversations AS svc").
			ColumnExpr("arb.id, agr.completed_at, arb.payload").
			Join("JOIN agent_runs AS agr ON agr.organization_id = svc.organization_id AND agr.scope_kind = ? AND agr.scope_id = svc.current_service_session_id", domain.AgentExecutionScopeServiceSession).
			Join("JOIN agent_run_blocks AS arb ON arb.organization_id = agr.organization_id AND arb.agent_run_id = agr.id").
			Where("svc.organization_id = ? AND svc.conversation_id = ?", identity.Organization.ID, conversationID).
			Where("agr.status IN (?)", bun.In([]domain.AgentRunStatus{
				domain.AgentRunStatusSucceeded, domain.AgentRunStatusFailed, domain.AgentRunStatusCancelled,
			})).
			Where("agr.completed_at IS NOT NULL").
			Where("arb.kind = ?", domain.AgentRunBlockToolCall).
			Where("COALESCE(arb.payload->'toolCall'->>'mcpServer', '') <> ''").
			OrderExpr("agr.created_at DESC, arb.position DESC").
			Scan(ctx, &rows); err != nil {
			return fmt.Errorf("load business queries: %w", err)
		}
		for _, row := range rows {
			var payload agentruntime.BlockPayload
			if err := json.Unmarshal(row.Payload, &payload); err != nil {
				return fmt.Errorf("decode business query block: %w", err)
			}
			// 未开始执行的调用以运行结束时间作为调用时间。
			calledAt := row.CompletedAt
			if payload.ToolCall.StartedAt != nil {
				calledAt = *payload.ToolCall.StartedAt
			}
			queries = append(queries, BusinessQuery{ID: row.ID, CalledAt: calledAt, ToolCall: *payload.ToolCall})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return queries, nil
}
