//go:build server

package knowledgebase

import (
	"context"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// DeleteDocumentAction 删除文档并释放原件。
type DeleteDocumentAction struct{ db *bun.DB }

// NewDeleteDocumentAction 创建文档删除操作。
func NewDeleteDocumentAction(db *bun.DB) *DeleteDocumentAction { return &DeleteDocumentAction{db: db} }

// Execute 将原件标记为待清理，并删除文档正文与文档关联。
func (a *DeleteDocumentAction) Execute(ctx context.Context, identity *servermodels.Identity, baseID, documentID string) error {
	return a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := lockKnowledgeBase(ctx, tx, identity.Organization.ID, baseID); err != nil {
			return err
		}
		if _, err := lockDocument(ctx, tx, baseID, documentID); err != nil {
			return err
		}
		if err := deleteSourceSegments(ctx, tx, documentID); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.File)(nil)).Set("status = ?", domain.FileStatusDeleting).Set("expires_at = now()").Set("updated_at = now()").Where("id IN (SELECT file_id FROM knowledge_documents WHERE id = ?)", documentID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*servermodels.KnowledgeDocumentContent)(nil)).Where("document_id = ?", documentID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model((*servermodels.KnowledgeDocument)(nil)).Where("id = ?", documentID).Exec(ctx)
		return err
	})
}
