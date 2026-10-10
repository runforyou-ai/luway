//go:build server

package processquery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// maxToolCallUpdates 是一次读取的过程更新条数上限。
const maxToolCallUpdates = 500

// ToolCallUpdate 是一条带序号的过程更新。
type ToolCallUpdate struct {
	Seq    int                   `bun:"seq"`
	Update domain.ToolCallUpdate `bun:"content,type:jsonb"`
}

// ToolCallProcess 是电脑执行的工具调用的当前状态、错误与序号不小于给定序号的过程更新；LocalAgent 是委派的本机 Agent 名称，其他调用为空。
type ToolCallProcess struct {
	Status     domain.AgentToolCallStatus
	Error      *string
	LocalAgent *string
	Updates    []ToolCallUpdate
}

// ToolCallProcessQuery 读取电脑执行的工具调用的过程更新。
type ToolCallProcessQuery struct {
	db *bun.DB
}

// NewToolCallProcessQuery 创建工具调用过程查询。
func NewToolCallProcessQuery(db *bun.DB) *ToolCallProcessQuery {
	return &ToolCallProcessQuery{db: db}
}

// Execute 校验调用所属会话的阅读资格后返回调用的状态与序号不小于 fromSeq 的过程更新，最多返回 maxToolCallUpdates 条。
func (q *ToolCallProcessQuery) Execute(ctx context.Context, identity *servermodels.Identity, callID string, fromSeq int) (ToolCallProcess, error) {
	if !str.IsUUID(callID) {
		return ToolCallProcess{}, ErrToolCallProcessUnavailable
	}
	var process ToolCallProcess
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var call struct {
			ConversationID string                     `bun:"conversation_id"`
			Status         domain.AgentToolCallStatus `bun:"status"`
			Error          *string                    `bun:"error"`
			LocalAgent     *string                    `bun:"local_agent"`
		}
		err := tx.NewSelect().Model((*servermodels.AgentToolCall)(nil)).
			ColumnExpr("agr.conversation_id::text, atc.status, atc.error, las.local_agent").
			Join("JOIN agent_runs AS agr ON agr.workspace_id = atc.workspace_id AND agr.id = atc.agent_run_id").
			Join("LEFT JOIN local_agent_sessions AS las ON las.workspace_id = atc.workspace_id AND las.id = atc.local_agent_session_id").
			Where("atc.workspace_id = ? AND atc.id = ? AND atc.computer_id IS NOT NULL", identity.Workspace.ID, callID).
			Scan(ctx, &call)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrToolCallProcessUnavailable
		} else if err != nil {
			return fmt.Errorf("load agent tool call: %w", err)
		}
		// 会话不可读与调用不存在对外是同一结果。
		if err := conversationaccess.RequireReadable(ctx, tx, identity, call.ConversationID); err != nil {
			if errors.Is(err, chatstate.ErrConversationNotFound) {
				return ErrToolCallProcessUnavailable
			}
			return err
		}
		process = ToolCallProcess{Status: call.Status, Error: call.Error, LocalAgent: call.LocalAgent, Updates: []ToolCallUpdate{}}
		if err := tx.NewSelect().Model((*servermodels.AgentToolCallUpdate)(nil)).
			Column("atcu.seq", "atcu.content").
			Where("atcu.tool_call_id = ? AND atcu.seq >= ?", callID, fromSeq).
			OrderExpr("atcu.seq").Limit(maxToolCallUpdates).
			Scan(ctx, &process.Updates); err != nil {
			return fmt.Errorf("load agent tool call updates: %w", err)
		}
		return nil
	})
	if err != nil {
		return ToolCallProcess{}, fmt.Errorf("read agent tool call process: %w", err)
	}
	return process, nil
}
