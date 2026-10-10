//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/localagent"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// StopLocalAgentAction 由可阅读会话的成员停止委派给本机 Agent 的一轮。
type StopLocalAgentAction struct {
	db *bun.DB
}

// NewStopLocalAgentAction 创建停止本机 Agent 的操作。
func NewStopLocalAgentAction(db *bun.DB) *StopLocalAgentAction {
	return &StopLocalAgentAction{db: db}
}

// Execute 释放这一轮所属的本机 Agent 会话：执行中的轮次在执行器下次领取时中止，尚未执行的轮次取消；会话已释放时不做任何事。
func (a *StopLocalAgentAction) Execute(ctx context.Context, identity *servermodels.Identity, callID string) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var turn struct {
			ConversationID string  `bun:"conversation_id"`
			SessionID      *string `bun:"local_agent_session_id"`
		}
		err := tx.NewSelect().Model((*servermodels.AgentToolCall)(nil)).
			ColumnExpr("agr.conversation_id::text, atc.local_agent_session_id::text").
			Join("JOIN agent_runs AS agr ON agr.workspace_id = atc.workspace_id AND agr.id = atc.agent_run_id").
			Where("atc.workspace_id = ? AND atc.id = ?", identity.Workspace.ID, callID).
			Scan(ctx, &turn)
		if errors.Is(err, sql.ErrNoRows) || err == nil && turn.SessionID == nil {
			return processquery.ErrToolCallProcessUnavailable
		} else if err != nil {
			return fmt.Errorf("load local agent turn: %w", err)
		}
		if err := conversationaccess.RequireReadable(ctx, tx, identity, turn.ConversationID); err != nil {
			if errors.Is(err, ErrConversationNotFound) {
				return processquery.ErrToolCallProcessUnavailable
			}
			return err
		}
		if _, err := chatstate.LockConversation(ctx, tx, identity.Workspace.ID, turn.ConversationID); err != nil {
			return err
		}
		return localagent.ReleaseWhere(ctx, tx, identity.Workspace.ID, "las.id = ?", *turn.SessionID)
	})
}
