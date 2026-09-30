//go:build server

package knowledgebase

import (
	"context"
	"fmt"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// DeleteKnowledgeBaseAction 删除企业知识库。
type DeleteKnowledgeBaseAction struct {
	db *bun.DB
}

// NewDeleteKnowledgeBaseAction 创建知识库删除操作。
func NewDeleteKnowledgeBaseAction(db *bun.DB) *DeleteKnowledgeBaseAction {
	return &DeleteKnowledgeBaseAction{db: db}
}

// Execute 删除当前企业中的指定知识库。
func (a *DeleteKnowledgeBaseAction) Execute(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID string) error {
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := lockKnowledgeBase(ctx, tx, identity.Organization.ID, knowledgeBaseID); err != nil {
			return err
		}
		// 锁定全部文档和问答条目，等待进行中的索引发布事务结束后再删除分段。
		var documents []servermodels.KnowledgeDocument
		if err := tx.NewSelect().Model(&documents).Where("kd.knowledge_base_id = ?", knowledgeBaseID).Order("kd.id").For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		var qaEntries []servermodels.KnowledgeQAEntry
		if err := tx.NewSelect().Model(&qaEntries).Where("kqe.knowledge_base_id = ?", knowledgeBaseID).Order("kqe.id").For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		if err := deleteKnowledgeBaseSegments(ctx, tx, knowledgeBaseID); err != nil {
			return err
		}
		// 文档原件在事务中释放，后台文件任务负责实际清理。
		if _, err := tx.NewUpdate().Model((*servermodels.File)(nil)).Set("status = ?", domain.FileStatusDeleting).Set("expires_at = now()").Set("updated_at = now()").Where("id IN (SELECT file_id FROM knowledge_documents WHERE knowledge_base_id = ?)", knowledgeBaseID).Exec(ctx); err != nil {
			return err
		}
		documentIDs := tx.NewSelect().Model((*servermodels.KnowledgeDocument)(nil)).Column("id").Where("knowledge_base_id = ?", knowledgeBaseID)
		if _, err := tx.NewDelete().Model((*servermodels.KnowledgeDocumentContent)(nil)).Where("document_id IN (?)", documentIDs).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*servermodels.KnowledgeDocument)(nil)).Where("knowledge_base_id = ?", knowledgeBaseID).Exec(ctx); err != nil {
			return err
		}
		// 先删除内容，再删除条目，全部操作持有知识库锁。
		entries := tx.NewSelect().Model((*servermodels.KnowledgeQAEntry)(nil)).Column("id").Where("knowledge_base_id = ?", knowledgeBaseID)
		if _, err := tx.NewDelete().Model((*servermodels.KnowledgeQAContent)(nil)).Where("entry_id IN (?)", entries).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*servermodels.KnowledgeQAEntry)(nil)).Where("knowledge_base_id = ?", knowledgeBaseID).Exec(ctx); err != nil {
			return err
		}
		// 为引用该知识库的 AI 员工与助理创建移除该知识库的新配置版本。
		if _, err := agentaction.RemoveKnowledgeBaseFromRevisions(ctx, tx, identity, knowledgeBaseID); err != nil {
			return err
		}
		// 从各网站渠道的帮助中心移除该知识库。
		if _, err := tx.NewDelete().Model((*servermodels.WebsiteChannelKnowledgeBase)(nil)).Where("organization_id = ? AND knowledge_base_id = ?", identity.Organization.ID, knowledgeBaseID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model((*servermodels.KnowledgeBase)(nil)).
			Where("organization_id = ?", identity.Organization.ID).
			Where("id = ?", knowledgeBaseID).
			Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete knowledge base: %w", err)
	}
	return nil
}
