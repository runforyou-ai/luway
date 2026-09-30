//go:build server

package knowledgebase

import (
	"context"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// SaveQAEntryAction 创建或更新完整问答并保留既有内容编号；新建、内容增删改或索引尚未成功时投递索引任务。
type SaveQAEntryAction struct {
	db         *bun.DB
	processing *QAProcessing
}

// NewSaveQAEntryAction 创建问答保存操作。
func NewSaveQAEntryAction(db *bun.DB, tasks servertask.TxEnqueuer) *SaveQAEntryAction {
	return &SaveQAEntryAction{db: db, processing: NewQAProcessing(db, tasks)}
}

// Execute 在同一事务中保存问答归属和全部内容，空编号表示新增。
func (a *SaveQAEntryAction) Execute(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID, entryID string, input QAInput) (*QARecord, error) {
	var output *QARecord
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var err error
		output, err = a.ExecuteInTx(ctx, tx, identity, knowledgeBaseID, entryID, input)
		return err
	})
	return output, err
}

// ExecuteInTx 在调用方已锁定活跃用户的事务中保存问答归属和全部内容，空编号表示新增。
func (a *SaveQAEntryAction) ExecuteInTx(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, knowledgeBaseID, entryID string, input QAInput) (*QARecord, error) {
	input, err := normalizeQAInput(input)
	if err != nil {
		return nil, err
	}
	base, err := lockKnowledgeBase(ctx, tx, identity.Organization.ID, knowledgeBaseID)
	if err != nil {
		return nil, err
	}
	if err := validateQAKnowledgeBase(base); err != nil {
		return nil, err
	}
	var entry *servermodels.KnowledgeQAEntry
	if entryID == "" {
		entry = &servermodels.KnowledgeQAEntry{KnowledgeBaseID: knowledgeBaseID, CreatedByUserID: identity.User.ID}
		if _, err := tx.NewInsert().Model(entry).Column("knowledge_base_id", "created_by_user_id").Returning("*").Exec(ctx); err != nil {
			return nil, err
		}
	} else {
		entry, err = lockQAEntry(ctx, tx, knowledgeBaseID, entryID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.NewUpdate().Model(entry).Set("updated_at = now()").WherePK().Returning("*").Exec(ctx); err != nil {
			return nil, err
		}
	}
	changed, err := saveQAContents(ctx, tx, entry.ID, input)
	if err != nil {
		return nil, err
	}
	// 新建、主问题、相似问题或答案增删改，或索引尚未成功时投递新任务。
	if entryID == "" || changed || entry.Status != domain.KnowledgeIndexSucceeded {
		if err := a.processing.enqueue(ctx, tx, identity.Organization.ID, base, entry); err != nil {
			return nil, err
		}
	}
	return loadQARecord(ctx, tx, entry)
}

// saveQAContents 按内容编号更新文本和顺序，删除被移除的相似问题，并返回主问题、相似问题或答案是否增删改。
func saveQAContents(ctx context.Context, tx bun.Tx, entryID string, input QAInput) (bool, error) {
	stored := make([]servermodels.KnowledgeQAContent, 0)
	if err := tx.NewSelect().Model(&stored).Where("kqc.entry_id = ?", entryID).Scan(ctx); err != nil {
		return false, err
	}
	byID := make(map[string]servermodels.KnowledgeQAContent)
	byKind := make(map[domain.KnowledgeQAContentKind]string)
	for _, content := range stored {
		byID[content.ID] = content
		byKind[content.Kind] = content.ID
	}
	// 在清理空项和重复文本前校验所有编号，拒绝引用其他问答或其他类型的内容。
	for _, question := range input.SimilarQuestions {
		if question.ID != "" {
			content, exists := byID[question.ID]
			if !exists || content.Kind != domain.KnowledgeQAContentSimilarQuestion {
				return false, &common.FieldError{Fields: map[string]common.FieldCode{"similarQuestions": ValidationQAContentInvalid}}
			}
		}
	}
	desired := []servermodels.KnowledgeQAContent{
		{ID: byKind[domain.KnowledgeQAContentPrimaryQuestion], Kind: domain.KnowledgeQAContentPrimaryQuestion, Content: input.Question},
		{ID: byKind[domain.KnowledgeQAContentAnswer], Kind: domain.KnowledgeQAContentAnswer, Content: input.Answer},
	}
	seen := map[string]bool{input.Question: true, "": true}
	for _, question := range input.SimilarQuestions {
		if seen[question.Content] {
			continue
		}
		seen[question.Content] = true
		desired = append(desired, servermodels.KnowledgeQAContent{ID: question.ID, Kind: domain.KnowledgeQAContentSimilarQuestion, Content: question.Content, SortOrder: len(desired) - 2})
	}
	retained := make([]string, 0, len(desired))
	changed := false
	for _, content := range desired {
		if content.ID == "" {
			content.EntryID = entryID
			if _, err := tx.NewInsert().Model(&content).Column("entry_id", "kind", "content", "sort_order").Returning("id").Exec(ctx); err != nil {
				return false, err
			}
			changed = true
		} else if previous := byID[content.ID]; previous.Content != content.Content || previous.SortOrder != content.SortOrder {
			if _, err := tx.NewUpdate().Model(&content).Set("content = ?", content.Content).Set("sort_order = ?", content.SortOrder).
				Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
				return false, err
			}
			changed = changed || previous.Content != content.Content
		}
		retained = append(retained, content.ID)
	}
	result, err := tx.NewDelete().Model((*servermodels.KnowledgeQAContent)(nil)).Where("entry_id = ?", entryID).Where("id NOT IN (?)", bun.In(retained)).Exec(ctx)
	if err != nil {
		return false, err
	}
	removed, err := result.RowsAffected()
	return changed || removed > 0, err
}
