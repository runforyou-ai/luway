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

// Execute 返回当前企业中按名称排序的 AI 员工。
func (q *ListKnowledgeBaseAgentsQuery) Execute(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID string) ([]AgentUsage, error) {
	knowledgeBase, err := loadKnowledgeBase(ctx, q.db, identity.Organization.ID, knowledgeBaseID)
	if err != nil {
		return nil, fmt.Errorf("list knowledge base agents: %w", err)
	}
	agents := make([]AgentUsage, 0)
	if err := q.db.NewSelect().TableExpr("agents AS a").
		Join("JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.organization_id = a.organization_id").
		Join("JOIN organization_identities AS oi ON oi.id = a.identity_id AND oi.organization_id = a.organization_id").
		ColumnExpr("a.id::text AS id, oi.display_name, a.status").
		Where("a.organization_id = ?", identity.Organization.ID).
		Where("ar.configuration->'knowledgeBaseIds' @> jsonb_build_array(?::text)", knowledgeBase.ID).
		OrderExpr("lower(oi.display_name) ASC, a.id ASC").
		Scan(ctx, &agents); err != nil {
		return nil, fmt.Errorf("list knowledge base agents: %w", err)
	}
	return agents, nil
}
