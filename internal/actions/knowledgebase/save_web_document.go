//go:build server

package knowledgebase

import (
	"context"
	"strings"
	"uuid"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
)

// CreateWebDocumentAction 保存网页导入的文档并安排首次抓取。
type CreateWebDocumentAction struct {
	db         *bun.DB
	processing *DocumentProcessing
}

// NewCreateWebDocumentAction 创建网页文档保存操作。
func NewCreateWebDocumentAction(db *bun.DB, tasks servertask.TxEnqueuer) *CreateWebDocumentAction {
	return &CreateWebDocumentAction{db: db, processing: NewDocumentProcessing(db, tasks)}
}

// Execute 在同一事务中保存网页文档并投递抓取任务。
func (a *CreateWebDocumentAction) Execute(ctx context.Context, identity *servermodels.Identity, baseID string, input WebDocumentInput) (*DocumentRecord, error) {
	input.Title = strings.TrimSpace(input.Title)
	address, err := webfetch.Normalize(input.SourceURL)
	if err != nil {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"sourceUrl": ValidationDocumentURLInvalid}}
	}
	var output *DocumentRecord
	err = serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		base, err := lockKnowledgeBase(ctx, tx, identity.Workspace.ID, baseID)
		if err != nil {
			return err
		}
		if base.Category != string(domain.KnowledgeBaseCategoryStandard) {
			return ErrDocumentUnsupported
		}
		document := &servermodels.KnowledgeDocument{ID: uuid.NewV7().String(), KnowledgeBaseID: baseID, SourceKind: domain.KnowledgeDocumentSourceWeb, Title: input.Title, SourceURL: address, Status: domain.KnowledgeIndexInitial, CreatedByUserID: identity.User.ID}
		if _, err := tx.NewInsert().Model(document).Value("created_at", "clock_timestamp()").Value("updated_at", "clock_timestamp()").Exec(ctx); err != nil {
			if pgerr.UniqueViolationOn(err, "knowledge_documents_source_url_unique") {
				return ErrDocumentURLDuplicate
			}
			return err
		}
		if err := a.processing.enqueue(ctx, tx, identity.Workspace.ID, base, true, document); err != nil {
			return err
		}
		output, err = loadDocumentRecord(ctx, tx, baseID, document.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}
