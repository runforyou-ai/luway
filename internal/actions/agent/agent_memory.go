//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrAgentMemoryNotFound 表示个人 AI 员工不存在指定记忆。
var ErrAgentMemoryNotFound = errors.New("agent memory not found")

// AgentMemory 定义个人 AI 员工的一条记忆。
type AgentMemory struct {
	ID          string    `bun:"id"`
	Name        string    `bun:"name"`
	Description string    `bun:"description"`
	Body        string    `bun:"body"`
	UpdatedAt   time.Time `bun:"updated_at"`
}

// AgentMemoryInput 定义负责人编辑记忆时提交的名称、说明与正文。
type AgentMemoryInput struct {
	Name        string
	Description string
	Body        string
}

// ListAgentMemoriesQuery 读取当前成员负责的个人 AI 员工的记忆。
type ListAgentMemoriesQuery struct{ db *bun.DB }

// NewListAgentMemoriesQuery 创建个人 AI 员工记忆列表查询。
func NewListAgentMemoriesQuery(db *bun.DB) *ListAgentMemoriesQuery {
	return &ListAgentMemoriesQuery{db: db}
}

// Execute 按最近更新顺序返回当前成员负责的个人 AI 员工的全部记忆。
func (q *ListAgentMemoriesQuery) Execute(ctx context.Context, identity *servermodels.Identity, agentID string) ([]AgentMemory, error) {
	if _, err := loadPersonalAgent(ctx, q.db, identity.Workspace.ID, agentID, identity.User.ID); err != nil {
		return nil, err
	}
	memories := make([]AgentMemory, 0)
	if err := q.db.NewSelect().Model((*servermodels.AgentMemory)(nil)).
		Column("id", "name", "description", "body", "updated_at").
		Where("am.workspace_id = ? AND am.agent_id = ?", identity.Workspace.ID, agentID).
		OrderExpr("am.updated_at DESC, am.path ASC").
		Scan(ctx, &memories); err != nil {
		return nil, fmt.Errorf("list personal agent memories: %w", err)
	}
	return memories, nil
}

// UpdateAgentMemoryAction 由负责人修改个人 AI 员工的一条记忆。
type UpdateAgentMemoryAction struct{ db *bun.DB }

// NewUpdateAgentMemoryAction 创建个人 AI 员工记忆编辑操作。
func NewUpdateAgentMemoryAction(db *bun.DB) *UpdateAgentMemoryAction {
	return &UpdateAgentMemoryAction{db: db}
}

// Execute 校验并保存记忆的名称、说明与正文，内容变化时刷新更新时间并通知负责人的其他客户端。
func (a *UpdateAgentMemoryAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID, memoryID string, input AgentMemoryInput) (*AgentMemory, error) {
	input = AgentMemoryInput{Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description), Body: strings.TrimSpace(input.Body)}
	memory := &AgentMemory{}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := lockOwnPersonalAgent(ctx, tx, identity, agentID); err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.AgentMemory)(nil)).
			Set("name = ?, description = ?, body = ?", input.Name, input.Description, input.Body).
			Where("workspace_id = ? AND agent_id = ? AND id = ?", identity.Workspace.ID, agentID, memoryID).
			Where("(name, description, body) IS DISTINCT FROM (?, ?, ?)", input.Name, input.Description, input.Body).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("update agent memory: %w", err)
		}
		if changed, _ := result.RowsAffected(); changed > 0 {
			realtime.Notify(ctx, realtime.UserAgentMemoryChanged(identity.Workspace.ID, identity.User.ID, agentID))
		}
		err = tx.NewSelect().Model((*servermodels.AgentMemory)(nil)).
			Column("id", "name", "description", "body", "updated_at").
			Where("am.workspace_id = ? AND am.agent_id = ? AND am.id = ?", identity.Workspace.ID, agentID, memoryID).
			Scan(ctx, memory)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAgentMemoryNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return memory, nil
}

// DeleteAgentMemoryAction 由负责人删除个人 AI 员工的一条记忆。
type DeleteAgentMemoryAction struct{ db *bun.DB }

// NewDeleteAgentMemoryAction 创建个人 AI 员工记忆删除操作。
func NewDeleteAgentMemoryAction(db *bun.DB) *DeleteAgentMemoryAction {
	return &DeleteAgentMemoryAction{db: db}
}

// Execute 删除当前成员负责的个人 AI 员工的一条记忆并通知负责人的其他客户端，记忆已不存在时视为删除成功。
func (a *DeleteAgentMemoryAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID, memoryID string) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := lockOwnPersonalAgent(ctx, tx, identity, agentID); err != nil {
			return err
		}
		result, err := tx.NewDelete().Model((*servermodels.AgentMemory)(nil)).
			Where("workspace_id = ? AND agent_id = ? AND id = ?", identity.Workspace.ID, agentID, memoryID).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("delete agent memory: %w", err)
		}
		if deleted, _ := result.RowsAffected(); deleted > 0 {
			realtime.Notify(ctx, realtime.UserAgentMemoryChanged(identity.Workspace.ID, identity.User.ID, agentID))
		}
		return nil
	})
}
