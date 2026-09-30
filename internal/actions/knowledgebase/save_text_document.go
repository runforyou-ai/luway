//go:build server

package knowledgebase

import (
	"context"
	"strings"
	"unicode/utf8"
	"uuid"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// SaveTextDocumentAction 创建或更新在线编写的文档；新建、正文变化或索引尚未成功时投递索引任务。
type SaveTextDocumentAction struct {
	db         *bun.DB
	processing *DocumentProcessing
}

// NewSaveTextDocumentAction 创建在线文档保存操作。
func NewSaveTextDocumentAction(db *bun.DB, tasks servertask.TxEnqueuer) *SaveTextDocumentAction {
	return &SaveTextDocumentAction{db: db, processing: NewDocumentProcessing(db, tasks)}
}

// Execute 在同一事务中保存文档归属、名称和正文，空编号表示新增。
func (a *SaveTextDocumentAction) Execute(ctx context.Context, identity *servermodels.Identity, baseID, documentID string, input TextDocumentInput) (*DocumentRecord, error) {
	input, fields := normalizeTextDocumentInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	var output *DocumentRecord
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
		document := &servermodels.KnowledgeDocument{}
		changed := documentID == ""
		if changed {
			document = &servermodels.KnowledgeDocument{ID: uuid.NewV7().String(), KnowledgeBaseID: baseID, SourceKind: domain.KnowledgeDocumentSourceText, Title: input.Title, Status: domain.KnowledgeIndexInitial, CreatedByUserID: identity.User.ID}
			if _, err := tx.NewInsert().Model(document).Value("created_at", "clock_timestamp()").Value("updated_at", "clock_timestamp()").Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewInsert().Model(&servermodels.KnowledgeDocumentContent{DocumentID: document.ID, Content: input.Content}).Column("document_id", "content").Exec(ctx); err != nil {
				return err
			}
		} else {
			document, err = lockDocument(ctx, tx, baseID, documentID)
			if err != nil {
				return err
			}
			if document.SourceKind != domain.KnowledgeDocumentSourceText {
				return ErrDocumentSourceUnsupported
			}
			stored := &servermodels.KnowledgeDocumentContent{}
			if err := tx.NewSelect().Model(stored).Where("kdc.document_id = ?", documentID).For("UPDATE").Scan(ctx); err != nil {
				return err
			}
			changed = stored.Content != input.Content
			if changed {
				if _, err := tx.NewUpdate().Model(stored).Set("content = ?", input.Content).Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
					return err
				}
			}
			document.Title = input.Title
			if _, err := tx.NewUpdate().Model(document).Column("title").Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
				return err
			}
		}
		// 新建、正文变化或索引尚未成功时按当前配置投递新任务。
		if changed || document.Status != domain.KnowledgeIndexSucceeded {
			if err := a.processing.enqueue(ctx, tx, identity.Organization.ID, base, false, document); err != nil {
				return err
			}
		}
		output, err = loadDocumentRecord(ctx, tx, baseID, document.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}

// RenameDocumentAction 修改在线文档或网页文档的名称。
type RenameDocumentAction struct{ db *bun.DB }

// NewRenameDocumentAction 创建文档改名操作。
func NewRenameDocumentAction(db *bun.DB) *RenameDocumentAction { return &RenameDocumentAction{db: db} }

// Execute 只更新名称，保留已发布批次和索引状态。
func (a *RenameDocumentAction) Execute(ctx context.Context, identity *servermodels.Identity, baseID, documentID, title string) (*DocumentRecord, error) {
	title = strings.TrimSpace(title)
	if fields := validateDocumentTitle(title); len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	var output *DocumentRecord
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := lockKnowledgeBase(ctx, tx, identity.Organization.ID, baseID); err != nil {
			return err
		}
		document, err := lockDocument(ctx, tx, baseID, documentID)
		if err != nil {
			return err
		}
		if document.SourceKind == domain.KnowledgeDocumentSourceFile {
			return ErrDocumentSourceUnsupported
		}
		if _, err := tx.NewUpdate().Model(document).Set("title = ?", title).Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
			return err
		}
		output, err = loadDocumentRecord(ctx, tx, baseID, documentID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}

// normalizeTextDocumentInput 规范化并校验在线文档的名称与正文。
func normalizeTextDocumentInput(input TextDocumentInput) (TextDocumentInput, map[string]common.FieldCode) {
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	fields := validateDocumentTitle(input.Title)
	if input.Content == "" {
		fields["content"] = ValidationDocumentContentRequired
	}
	return input, fields
}

// validateDocumentTitle 校验文档名称的必填与长度。
func validateDocumentTitle(title string) map[string]common.FieldCode {
	fields := make(map[string]common.FieldCode)
	if title == "" {
		fields["title"] = ValidationDocumentTitleRequired
	} else if utf8.RuneCountInString(title) > domain.KnowledgeDocumentTitleMaxLength {
		fields["title"] = ValidationDocumentTitleTooLong
	}
	return fields
}
