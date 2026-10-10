//go:build server

package processquery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// AuthorizeRunStreamQuery 校验成员对一次运行所属会话的阅读资格。
type AuthorizeRunStreamQuery struct {
	db *bun.DB
}

// NewAuthorizeRunStreamQuery 创建运行过程流授权查询。
func NewAuthorizeRunStreamQuery(db *bun.DB) *AuthorizeRunStreamQuery {
	return &AuthorizeRunStreamQuery{db: db}
}

// Execute 返回运行所属会话编号；运行不存在、不属于本企业或会话不可读时返回 ErrRunProcessUnavailable。
func (q *AuthorizeRunStreamQuery) Execute(ctx context.Context, identity *servermodels.Identity, runID string) (string, error) {
	if !str.IsUUID(runID) {
		return "", ErrRunProcessUnavailable
	}
	var conversationID string
	err := q.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Column("agr.conversation_id").
		Where("agr.workspace_id = ? AND agr.id = ?", identity.Workspace.ID, runID).
		Scan(ctx, &conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRunProcessUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("load agent run conversation: %w", err)
	}
	// 会话不可读与运行不存在对外是同一结果，不暴露运行是否存在。
	if err := conversationaccess.RequireReadable(ctx, q.db, identity, conversationID); err != nil {
		if errors.Is(err, chatstate.ErrConversationNotFound) {
			return "", ErrRunProcessUnavailable
		}
		return "", fmt.Errorf("authorize agent run stream: %w", err)
	}
	return conversationID, nil
}

// State 返回已校验阅读资格的运行持久状态和执行位置。
func (q *AuthorizeRunStreamQuery) State(ctx context.Context, identity *servermodels.Identity, runID string) (servermodels.AgentRun, error) {
	if _, err := q.Execute(ctx, identity, runID); err != nil {
		return servermodels.AgentRun{}, err
	}
	var run servermodels.AgentRun
	err := q.db.NewSelect().Model(&run).Column("id", "status", "task_instance_id", "task_attempt").Where("agr.workspace_id = ? AND agr.id = ?", identity.Workspace.ID, runID).Scan(ctx)
	return run, err
}
