//go:build server

package localagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// Turn 是交给本机 Agent 会话的一轮：所属会话、AI 员工、电脑、工具调用与填入会话默认文件夹后的操作。
type Turn struct {
	WorkspaceID    string
	ConversationID string
	AgentID        string
	ComputerID     string
	CallID         string
	Operation      domain.ComputerOperation
	// From 是交出前调用应处的状态：运行中直接交出的调用为执行中，批准后交出的调用为待执行。
	From domain.AgentToolCallStatus
}

// Dispatch 在会话锁内把一轮交给会话中该本机 Agent 的会话：要求新会话或原会话在另一台电脑上时释放原会话，没有可续接的会话时新建；
// 调用改为待执行并关联会话，运行中直接交出的调用记为交出后运行继续，操作带上会话编号，随后通知执行器领取；本机 Agent 返回的 ACP 会话编号在领取时填入。电脑离线或已撤销时不交出并返回 false。
func Dispatch(ctx context.Context, tx bun.Tx, turn Turn) (bool, error) {
	if _, err := chatstate.LockConversation(ctx, tx, turn.WorkspaceID, turn.ConversationID); err != nil {
		return false, err
	}
	online, err := tx.NewSelect().Model((*servermodels.Computer)(nil)).
		Where("cmp.id = ? AND cmp.workspace_id = ? AND cmp.revoked_at IS NULL", turn.ComputerID, turn.WorkspaceID).
		Where("cmp.last_seen_at > now() - make_interval(secs => ?)", domain.ComputerPresenceTimeout.Seconds()).
		For("SHARE").Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("check computer presence: %w", err)
	}
	if !online {
		return false, nil
	}
	session, err := openSession(ctx, tx, turn)
	if err != nil {
		return false, err
	}
	operation := turn.Operation
	operation.Session = session.ID
	result, err := tx.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("computer_id = ?", turn.ComputerID).
		Set("operation = ?", domain.ComputerCall{Operation: operation}).
		Set("local_agent_session_id = ?", session.ID).
		Set("status = ?", domain.AgentToolCallQueued).
		Set("handover = COALESCE(handover, ?)", einorun.HandoverDetached).
		Set("trace_id = ?", support.NilIfZero(logscope.From(ctx).TraceID)).
		Set("started_at = NULL").
		Where("id = ? AND workspace_id = ? AND status = ?", turn.CallID, turn.WorkspaceID, turn.From).
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("dispatch local agent turn: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return false, fmt.Errorf("dispatch local agent turn: %w", err)
	} else if affected == 0 {
		return false, fmt.Errorf("local agent turn tool call is not %s", turn.From)
	}
	realtime.Notify(ctx, realtime.ComputerWork(turn.WorkspaceID, turn.ComputerID))
	return true, nil
}

// openSession 返回这一轮所属的本机 Agent 会话并记录最近一轮的时间：续接会话中该 AI 员工与本机 Agent 在同一台电脑上的会话，
// 要求新会话或原会话在另一台电脑上时释放原会话后新建。调用方已锁定会话。
func openSession(ctx context.Context, tx bun.Tx, turn Turn) (*servermodels.LocalAgentSession, error) {
	session := &servermodels.LocalAgentSession{}
	err := tx.NewSelect().Model(session).
		Where("las.workspace_id = ? AND las.conversation_id = ? AND las.agent_id = ? AND las.local_agent = ? AND las.status = ?",
			turn.WorkspaceID, turn.ConversationID, turn.AgentID, turn.Operation.LocalAgent, domain.LocalAgentSessionActive).
		For("UPDATE").Scan(ctx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		session = nil
	case err != nil:
		return nil, fmt.Errorf("load local agent session: %w", err)
	case turn.Operation.NewSession || session.ComputerID != turn.ComputerID:
		if err := Release(ctx, tx, turn.WorkspaceID, []*servermodels.LocalAgentSession{session}); err != nil {
			return nil, err
		}
		session = nil
	}
	if session == nil {
		session = &servermodels.LocalAgentSession{
			ID: uuid.NewV7().String(), WorkspaceID: turn.WorkspaceID, ConversationID: turn.ConversationID, AgentID: turn.AgentID,
			ComputerID: turn.ComputerID, LocalAgent: turn.Operation.LocalAgent, Status: string(domain.LocalAgentSessionActive),
		}
		if _, err := tx.NewInsert().Model(session).Exec(ctx); err != nil {
			return nil, fmt.Errorf("create local agent session: %w", err)
		}
		return session, nil
	}
	if _, err := tx.NewUpdate().Model(session).Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("touch local agent session: %w", err)
	}
	return session, nil
}
