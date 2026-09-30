//go:build server

package knowledgebase

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetKnowledgeBaseQuery 查询当前企业的指定知识库。
type GetKnowledgeBaseQuery struct {
	db *bun.DB
}

// NewGetKnowledgeBaseQuery 创建知识库详情查询。
func NewGetKnowledgeBaseQuery(db *bun.DB) *GetKnowledgeBaseQuery {
	return &GetKnowledgeBaseQuery{db: db}
}

// Execute 返回指定知识库详情。
func (q *GetKnowledgeBaseQuery) Execute(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID string) (*Record, error) {
	record, err := loadKnowledgeBaseRecord(ctx, q.db, identity.Organization.ID, knowledgeBaseID)
	if err != nil {
		return nil, fmt.Errorf("get knowledge base: %w", err)
	}
	return record, nil
}
