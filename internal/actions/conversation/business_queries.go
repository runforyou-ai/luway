//go:build server

package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// BusinessQuery 表示 AI 客服在一次运行中调用业务查询工具的记录。
type BusinessQuery struct {
	ID       string
	CalledAt time.Time
	ToolCall agentcontract.ToolCall
}

// ListBusinessQueriesQuery 读取客户会话当前客服周期内 AI 客服查询业务系统的记录。
type ListBusinessQueriesQuery struct{ db *bun.DB }

// NewListBusinessQueriesQuery 创建业务查询记录查询。
func NewListBusinessQueriesQuery(db *bun.DB) *ListBusinessQueriesQuery {
	return &ListBusinessQueriesQuery{db: db}
}

// Execute 按调用时间倒序返回当前周期已进入终态的 AI 客服运行中的业务系统工具调用。
func (q *ListBusinessQueriesQuery) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) ([]BusinessQuery, error) {
	queries := make([]BusinessQuery, 0)
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := conversationaccess.RequireReadable(ctx, tx, identity, conversationID); err != nil {
			return err
		}
		rows := make([]struct {
			BlockID     string                     `bun:"block_id"`
			CompletedAt time.Time                  `bun:"run_completed_at"`
			Call        servermodels.AgentToolCall `bun:",embed"`
		}, 0)
		if err := tx.NewSelect().
			TableExpr("service_conversations AS svc").
			ColumnExpr("arb.id AS block_id, agr.completed_at AS run_completed_at, atc.*").
			Join("JOIN agent_runs AS agr ON agr.workspace_id = svc.workspace_id AND agr.scope_kind = ? AND agr.scope_id = svc.current_service_session_id", domain.AgentExecutionScopeServiceSession).
			Join("JOIN agent_run_blocks AS arb ON arb.workspace_id = agr.workspace_id AND arb.agent_run_id = agr.id").
			Join("JOIN agent_tool_calls AS atc ON atc.workspace_id = arb.workspace_id AND atc.id = arb.tool_call_id").
			Where("svc.workspace_id = ? AND svc.conversation_id = ?", identity.Workspace.ID, conversationID).
			Where("agr.status IN (?)", bun.List([]domain.AgentRunStatus{
				domain.AgentRunStatusSucceeded, domain.AgentRunStatusFailed, domain.AgentRunStatusCancelled,
			})).
			Where("agr.completed_at IS NOT NULL").
			Where("atc.source = ?", domain.AgentToolSourceBusinessSystem).
			OrderExpr("agr.created_at DESC, arb.position DESC").
			Scan(ctx, &rows); err != nil {
			return fmt.Errorf("load business queries: %w", err)
		}
		for _, row := range rows {
			call := agentprocess.ToolCall(row.Call)
			// 未开始执行的调用以运行结束时间作为调用时间。
			queries = append(queries, BusinessQuery{ID: row.BlockID, CalledAt: support.DerefOr(call.StartedAt, row.CompletedAt), ToolCall: call})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return queries, nil
}
