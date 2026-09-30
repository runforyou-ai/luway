//go:build server

package knowledgebase

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// validateQAKnowledgeBase 确认当前知识库支持本地问答维护。
func validateQAKnowledgeBase(record *servermodels.KnowledgeBase) error {
	if record.Category != string(domain.KnowledgeBaseCategoryQA) {
		return ErrQAUnsupported
	}
	return nil
}

// loadQAEntry 读取指定知识库中的问答记录。
func loadQAEntry(ctx context.Context, db bun.IDB, knowledgeBaseID, entryID string) (*servermodels.KnowledgeQAEntry, error) {
	return queryQAEntry(ctx, db, knowledgeBaseID, entryID, false)
}

// lockQAEntry 读取并锁定指定知识库中的问答记录，供修改内容、投递索引任务和删除使用。
func lockQAEntry(ctx context.Context, db bun.IDB, knowledgeBaseID, entryID string) (*servermodels.KnowledgeQAEntry, error) {
	return queryQAEntry(ctx, db, knowledgeBaseID, entryID, true)
}

// queryQAEntry 按知识库读取问答记录，按需持有行锁。
func queryQAEntry(ctx context.Context, db bun.IDB, knowledgeBaseID, entryID string, lock bool) (*servermodels.KnowledgeQAEntry, error) {
	if !common.ValidUUID(entryID) {
		return nil, ErrQANotFound
	}
	record := &servermodels.KnowledgeQAEntry{}
	query := db.NewSelect().Model(record).Where("kqe.id = ?", entryID).Where("kqe.knowledge_base_id = ?", knowledgeBaseID)
	if lock {
		query = query.For("UPDATE")
	}
	err := query.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrQANotFound
	}
	return record, err
}

// loadQARecord 汇总同一问答的主问题、相似问题和答案。
func loadQARecord(ctx context.Context, db bun.IDB, entry *servermodels.KnowledgeQAEntry) (*QARecord, error) {
	contents := make([]servermodels.KnowledgeQAContent, 0)
	if err := db.NewSelect().Model(&contents).Where("kqc.entry_id = ?", entry.ID).
		OrderExpr("kqc.sort_order ASC, kqc.id ASC").Scan(ctx); err != nil {
		return nil, err
	}
	record := &QARecord{ID: entry.ID, UpdatedAt: entry.UpdatedAt,
		CreatedAt: entry.CreatedAt, SimilarQuestions: make([]QASimilarQuestion, 0)}
	for _, content := range contents {
		switch content.Kind {
		case domain.KnowledgeQAContentPrimaryQuestion:
			record.Question = content.Content
		case domain.KnowledgeQAContentAnswer:
			record.Answer = content.Content
		case domain.KnowledgeQAContentSimilarQuestion:
			record.SimilarQuestions = append(record.SimilarQuestions, QASimilarQuestion{ID: content.ID, Content: content.Content})
		}
	}
	return record, nil
}

// normalizeQAInput 校验必填字段并规范化相似问题编号和文本。
func normalizeQAInput(input QAInput) (QAInput, error) {
	input.Question = strings.TrimSpace(input.Question)
	input.Answer = strings.TrimSpace(input.Answer)
	var valid bool
	fields := make(map[string]common.FieldCode)
	if input.Question == "" {
		fields["question"] = ValidationQAQuestionRequired
	}
	if input.Answer == "" {
		fields["answer"] = ValidationQAAnswerRequired
	}
	questions := make([]QASimilarQuestion, 0, len(input.SimilarQuestions))
	ids := make(map[string]bool)
	for _, question := range input.SimilarQuestions {
		question.Content = strings.TrimSpace(question.Content)
		if question.ID != "" {
			question.ID, valid = common.NormalizeUUID(question.ID)
			if !valid || ids[question.ID] {
				fields["similarQuestions"] = ValidationQAContentInvalid
			}
			ids[question.ID] = true
		}
		questions = append(questions, question)
	}
	input.SimilarQuestions = questions
	if len(fields) != 0 {
		return QAInput{}, &common.FieldError{Fields: fields}
	}
	return input, nil
}
