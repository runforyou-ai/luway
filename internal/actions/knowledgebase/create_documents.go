//go:build server

package knowledgebase

import (
	"context"
	"database/sql"
	"errors"
	"uuid"

	fileaction "github.com/runforyou-ai/cervi/internal/actions/file"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// CreateDocumentsAction 激活上传原件并创建知识文档。
type CreateDocumentsAction struct {
	db         *bun.DB
	processing *DocumentProcessing
}

// NewCreateDocumentsAction 创建文档批次保存操作。
func NewCreateDocumentsAction(db *bun.DB, tasks servertask.TxEnqueuer) *CreateDocumentsAction {
	return &CreateDocumentsAction{db: db, processing: NewDocumentProcessing(db, tasks)}
}

// Execute 在同一事务中保存最多十个文件，文件编号用于重复提交幂等。
func (a *CreateDocumentsAction) Execute(ctx context.Context, identity *servermodels.Identity, baseID string, fileIDs []string) ([]DocumentRecord, error) {
	if len(fileIDs) == 0 || len(fileIDs) > 10 {
		return nil, ErrDocumentBatchInvalid
	}
	ids, valid := common.NormalizeUUIDs(fileIDs)
	if !valid || len(ids) != len(fileIDs) {
		return nil, ErrDocumentBatchInvalid
	}
	output := make([]DocumentRecord, 0, len(ids))
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		base, err := lockKnowledgeBase(ctx, tx, identity.Organization.ID, baseID)
		if err != nil {
			return err
		}
		if base.Category != string(domain.KnowledgeBaseCategoryStandard) {
			return ErrDocumentUnsupported
		}
		for _, fileID := range ids {
			file := &servermodels.File{}
			err := tx.NewSelect().Model(file).ColumnExpr("f.*").ColumnExpr("(f.expires_at IS NULL OR f.expires_at <= now()) AS expired").Where("f.id = ? AND f.organization_id = ? AND f.created_by_user_id = ? AND f.purpose = ?", fileID, identity.Organization.ID, identity.User.ID, domain.FilePurposeKnowledgeDocument).For("UPDATE").Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return fileaction.ErrFileNotFound
			}
			if err != nil {
				return err
			}
			if domain.KnowledgeDocumentContentType(file.OriginalName) == "" {
				return ErrDocumentUnsupported
			}
			// 已保存的原件返回既有文档。
			existing := &servermodels.KnowledgeDocument{}
			err = tx.NewSelect().Model(existing).Where("kd.file_id = ?", fileID).Scan(ctx)
			if err == nil {
				if existing.KnowledgeBaseID != baseID {
					return fileaction.ErrFileNotFound
				}
				record, err := loadDocumentRecord(ctx, tx, baseID, existing.ID)
				if err != nil {
					return err
				}
				output = append(output, *record)
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if file.Status != string(domain.FileStatusUploaded) || file.Expired {
				return fileaction.ErrFileNotFound
			}
			document := &servermodels.KnowledgeDocument{ID: uuid.NewV7().String(), KnowledgeBaseID: baseID, SourceKind: domain.KnowledgeDocumentSourceFile, FileID: fileID, Status: domain.KnowledgeIndexInitial, CreatedByUserID: identity.User.ID}
			if _, err := tx.NewInsert().Model(document).Value("created_at", "clock_timestamp()").Value("updated_at", "clock_timestamp()").Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Model(file).Set("status = ?", domain.FileStatusActive).Set("expires_at = NULL").Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
				return err
			}
			if err := a.processing.enqueue(ctx, tx, identity.Organization.ID, base, false, document); err != nil {
				return err
			}
			record, err := loadDocumentRecord(ctx, tx, baseID, document.ID)
			if err != nil {
				return err
			}
			output = append(output, *record)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}
