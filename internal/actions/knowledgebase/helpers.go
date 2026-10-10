//go:build server

package knowledgebase

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// loadKnowledgeBase 读取当前企业中的知识库。
func loadKnowledgeBase(ctx context.Context, db bun.IDB, workspaceID, knowledgeBaseID string) (*servermodels.KnowledgeBase, error) {
	knowledgeBase := &servermodels.KnowledgeBase{}
	if err := db.NewSelect().
		Model(knowledgeBase).
		Where("kb.id = ?", knowledgeBaseID).
		Where("kb.workspace_id = ?", workspaceID).
		Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return knowledgeBase, nil
}

// loadKnowledgeBaseRecord 读取知识库详情。
func loadKnowledgeBaseRecord(ctx context.Context, db bun.IDB, workspaceID, knowledgeBaseID string) (*Record, error) {
	knowledgeBase, err := loadKnowledgeBase(ctx, db, workspaceID, knowledgeBaseID)
	if err != nil {
		return nil, err
	}
	record := recordFromModel(*knowledgeBase)
	return &record, nil
}

// recordFromModel 转换知识库存储模型。
func recordFromModel(knowledgeBase servermodels.KnowledgeBase) Record {
	return Record{
		ID: knowledgeBase.ID, Name: knowledgeBase.Name, Category: domain.KnowledgeBaseCategory(knowledgeBase.Category),
		Description:             knowledgeBase.Description,
		EmbeddingModelID:        knowledgeBase.EmbeddingModelID,
		EmbeddingDimension:      knowledgeBase.EmbeddingDimension,
		ChunkLength:             knowledgeBase.ChunkLength,
		ChunkOverlap:            knowledgeBase.ChunkOverlap,
		RetrievalCount:          knowledgeBase.RetrievalCount,
		RetrievalScoreThreshold: knowledgeBase.RetrievalScoreThreshold,
		RerankModelID:           knowledgeBase.RerankModelID,
		CreatedAt:               knowledgeBase.CreatedAt, UpdatedAt: knowledgeBase.UpdatedAt,
	}
}

// rowsAffectedOne 校验写操作确实命中一行。
func rowsAffectedOne(result sql.Result, notFound error) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return notFound
	}
	return nil
}

// lockKnowledgeBase 串行化同一知识库的内容和归属变更。
func lockKnowledgeBase(ctx context.Context, db bun.IDB, workspaceID, knowledgeBaseID string) (*servermodels.KnowledgeBase, error) {
	record := &servermodels.KnowledgeBase{}
	err := db.NewSelect().Model(record).Where("kb.id = ?", knowledgeBaseID).
		Where("kb.workspace_id = ?", workspaceID).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return record, err
}
