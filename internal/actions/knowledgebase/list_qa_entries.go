//go:build server

package knowledgebase

import (
	"context"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListQAEntriesQuery 查询本地问答库中的内容。
type ListQAEntriesQuery struct{ db *bun.DB }

// NewListQAEntriesQuery 创建问答列表查询。
func NewListQAEntriesQuery(db *bun.DB) *ListQAEntriesQuery { return &ListQAEntriesQuery{db: db} }

// Execute 按主问题或相似问题返回稳定分页的问答列表。
func (q *ListQAEntriesQuery) Execute(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID string, input QAListInput) (QAListOutput, error) {
	base, err := loadKnowledgeBase(ctx, q.db, identity.Organization.ID, knowledgeBaseID)
	if err != nil {
		return QAListOutput{}, err
	}
	if err := validateQAKnowledgeBase(base); err != nil {
		return QAListOutput{}, err
	}
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		return QAListOutput{}, ErrPageSizeInvalid
	}
	records := make([]QASummary, 0)
	query := q.db.NewSelect().TableExpr("knowledge_qa_entries AS kqe").
		ColumnExpr("kqe.id, kqe.status, kqe.failure_code, kqe.updated_at, primary_content.content AS question, answer_content.content AS answer").
		ColumnExpr("ARRAY(SELECT similar_content.content FROM knowledge_qa_contents AS similar_content WHERE similar_content.entry_id = kqe.id AND similar_content.kind = ? ORDER BY similar_content.sort_order, similar_content.id) AS similar_questions", domain.KnowledgeQAContentSimilarQuestion).
		Join("JOIN knowledge_qa_contents AS primary_content ON primary_content.entry_id = kqe.id AND primary_content.kind = ?", domain.KnowledgeQAContentPrimaryQuestion).
		Join("JOIN knowledge_qa_contents AS answer_content ON answer_content.entry_id = kqe.id AND answer_content.kind = ?", domain.KnowledgeQAContentAnswer).
		Where("kqe.knowledge_base_id = ?", knowledgeBaseID)
	if keyword := strings.TrimSpace(input.Keyword); keyword != "" {
		query = query.Where("EXISTS (SELECT 1 FROM knowledge_qa_contents AS matched WHERE matched.entry_id = kqe.id AND matched.kind IN (?, ?) AND matched.content ILIKE ?)", domain.KnowledgeQAContentPrimaryQuestion, domain.KnowledgeQAContentSimilarQuestion, common.ContainsPattern(keyword))
	}
	total, err := query.OrderExpr("kqe.updated_at DESC, kqe.id DESC").Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).ScanAndCount(ctx, &records)
	return QAListOutput{Entries: records, Page: input.Page, PageSize: input.PageSize, Total: total}, err
}
