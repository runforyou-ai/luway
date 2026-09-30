//go:build server

// Package knowledgebase 实现企业知识库的查询和操作。
package knowledgebase

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListKnowledgeBasesQuery 查询当前企业的知识库。
type ListKnowledgeBasesQuery struct {
	db *bun.DB
}

// NewListKnowledgeBasesQuery 创建知识库列表查询。
func NewListKnowledgeBasesQuery(db *bun.DB) *ListKnowledgeBasesQuery {
	return &ListKnowledgeBasesQuery{db: db}
}

// Execute 返回当前企业的全部知识库。
func (q *ListKnowledgeBasesQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Record, error) {
	models := make([]servermodels.KnowledgeBase, 0)
	if err := q.db.NewSelect().
		Model(&models).
		Where("kb.organization_id = ?", identity.Organization.ID).
		OrderExpr("lower(kb.name) ASC, kb.id ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list knowledge bases: %w", err)
	}
	records := make([]Record, 0, len(models))
	for _, model := range models {
		records = append(records, recordFromModel(model))
	}
	return records, nil
}
