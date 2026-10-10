//go:build server

package knowledgebase

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// AgentUsage 表示当前配置版本绑定知识库的 AI 员工。
type AgentUsage struct {
	ID          string                `bun:"id"`
	DisplayName string                `bun:"display_name"`
	Status      domain.IdentityStatus `bun:"status"`
}

// ListKnowledgeBaseAgentsQuery 查询当前配置版本绑定指定知识库的 AI 员工。
type ListKnowledgeBaseAgentsQuery struct {
	db *bun.DB
}

// NewListKnowledgeBaseAgentsQuery 创建知识库 AI 员工查询。
func NewListKnowledgeBaseAgentsQuery(db *bun.DB) *ListKnowledgeBaseAgentsQuery {
	return &ListKnowledgeBaseAgentsQuery{db: db}
}

// Execute 返回当前配置版本绑定该知识库的 AI 员工，按名称排序。
func (q *ListKnowledgeBaseAgentsQuery) Execute(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID string) ([]AgentUsage, error) {
	knowledgeBase, err := loadKnowledgeBase(ctx, q.db, identity.Workspace.ID, knowledgeBaseID)
	if err != nil {
		return nil, fmt.Errorf("list knowledge base agents: %w", err)
	}
	agents := make([]AgentUsage, 0)
	if err := q.db.NewSelect().TableExpr("agents AS a").
		Join("JOIN agent_revision_knowledge_bases AS arkb ON arkb.workspace_id = a.workspace_id AND arkb.agent_id = a.id AND arkb.revision_id = a.active_revision_id").
		Join("JOIN workspace_identities AS oi ON oi.id = a.identity_id AND oi.workspace_id = a.workspace_id").
		ColumnExpr("a.id::text AS id, oi.display_name, a.status").
		Where("a.workspace_id = ?", identity.Workspace.ID).
		Where("arkb.knowledge_base_id = ?", knowledgeBase.ID).
		OrderExpr("lower(oi.display_name) ASC, a.id ASC").
		Scan(ctx, &agents); err != nil {
		return nil, fmt.Errorf("list knowledge base agents: %w", err)
	}
	return agents, nil
}
