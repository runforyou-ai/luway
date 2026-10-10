//go:build server

// Package localagent 维护 AI 员工委派给电脑上本机 Agent 的会话：把一轮交给会话、释放会话并取消其中尚未执行的轮次与等待处理的权限请求。
package localagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// ReleaseWhere 在调用方事务中释放满足条件的活跃本机 Agent 会话，条件中会话别名为 las：先按会话编号顺序锁定所属会话并登记时间线变化，
// 再把会话改为已释放、取消尚未领取的轮次与等待处理的权限请求，并通知相关电脑；执行中的轮次在执行器下次领取时中止。
func ReleaseWhere(ctx context.Context, tx bun.Tx, workspaceID, condition string, args ...any) error {
	conversations := tx.NewSelect().Model((*servermodels.LocalAgentSession)(nil)).Column("las.conversation_id").
		Where("las.workspace_id = ? AND las.status = ?", workspaceID, domain.LocalAgentSessionActive).
		Where(condition, args...)
	if _, err := chatstate.TouchConversations(ctx, tx, workspaceID, conversations, domain.ConversationChangeTimeline); err != nil {
		return err
	}
	var sessions []*servermodels.LocalAgentSession
	if err := tx.NewSelect().Model(&sessions).
		Where("las.workspace_id = ? AND las.status = ?", workspaceID, domain.LocalAgentSessionActive).
		Where(condition, args...).
		OrderExpr("las.id").For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("lock local agent sessions: %w", err)
	}
	return Release(ctx, tx, workspaceID, sessions)
}

// Release 把已锁定的会话改为已释放，取消其尚未领取的轮次与等待处理的权限请求，并通知相关电脑；调用方已锁定所属会话。
func Release(ctx context.Context, tx bun.Tx, workspaceID string, sessions []*servermodels.LocalAgentSession) error {
	if len(sessions) == 0 {
		return nil
	}
	ids := arr.Map(sessions, func(session *servermodels.LocalAgentSession) string { return session.ID })
	if _, err := tx.NewUpdate().Model((*servermodels.LocalAgentSession)(nil)).
		Set("status = ?", domain.LocalAgentSessionReleased).
		Where("workspace_id = ? AND id IN (?)", workspaceID, bun.List(ids)).
		Exec(ctx); err != nil {
		return fmt.Errorf("release local agent sessions: %w", err)
	}
	// 取消的权限请求从处理人的待处理中移除。
	var assignees []string
	if err := tx.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("status = ?", domain.AgentToolCallCancelled).
		Set("completed_at = now()").
		Where("workspace_id = ? AND local_agent_session_id IN (?)", workspaceID, bun.List(ids)).
		Where("status IN (?)", bun.List([]domain.AgentToolCallStatus{domain.AgentToolCallQueued, domain.AgentToolCallAwaitingDecision})).
		Returning("COALESCE(assignee_subject_id::text, '')").
		Scan(ctx, &assignees); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("cancel local agent session turns: %w", err)
	}
	assignees = slices.DeleteFunc(assignees, func(id string) bool { return id == "" })
	if err := agentprocess.NotifyDecisionSubjects(ctx, tx, workspaceID, assignees...); err != nil {
		return err
	}
	for _, session := range sessions {
		realtime.Notify(ctx, realtime.ComputerWork(workspaceID, session.ComputerID))
	}
	return nil
}
