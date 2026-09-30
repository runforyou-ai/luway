//go:build server

package knowledgebase

import (
	"context"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/textsplit"
	"github.com/uptrace/bun"
)

const ProcessQAEntryActionName = "knowledge.qa_entry.process"

// ProcessQAInput 固定本次问答索引任务的来源和向量参数，问题与答案在执行时读取。
type ProcessQAInput struct {
	OrganizationID           string `json:"organizationId"`
	KnowledgeBaseID          string `json:"knowledgeBaseId"`
	EntryID                  string `json:"entryId"`
	ProcessingID             string `json:"processingId"`
	EmbeddingProviderID      string `json:"embeddingProviderId"`
	EmbeddingModelIdentifier string `json:"embeddingModelIdentifier"`
	EmbeddingDimension       int    `json:"embeddingDimension"`
}

// ProcessQAEntryAction 把问答条目的主问题、相似问题和答案分段、向量化并发布批次。
type ProcessQAEntryAction struct {
	db       *bun.DB
	embedder segmentEmbedder
}

// NewProcessQAEntryAction 创建问答索引任务。
func NewProcessQAEntryAction(db *bun.DB, embedder segmentEmbedder) *ProcessQAEntryAction {
	return &ProcessQAEntryAction{db: db, embedder: embedder}
}

// Execute 执行当前问答任务：切段并向量化后，在同一事务中替换分段并发布批次。
func (a *ProcessQAEntryAction) Execute(ctx context.Context, input ProcessQAInput) error {
	started := time.Now()
	current, err := a.setStage(ctx, input, domain.KnowledgeIndexSplitting)
	if err != nil || !current {
		return err
	}
	slog.Info("知识问答索引开始", "entry_id", input.EntryID, "processing_id", input.ProcessingID)
	// 主问题和相似问题各成一段，答案按固定长度切段，位置在批次内连续。
	contents := make([]servermodels.KnowledgeQAContent, 0)
	err = a.db.NewSelect().Model(&contents).Where("kqc.entry_id = ?", input.EntryID).
		OrderExpr("CASE kqc.kind WHEN ? THEN 0 WHEN ? THEN 1 ELSE 2 END, kqc.sort_order, kqc.id", domain.KnowledgeQAContentPrimaryQuestion, domain.KnowledgeQAContentSimilarQuestion).Scan(ctx)
	if err != nil {
		return err
	}
	segments := make([]textsplit.Segment, 0, len(contents))
	for _, content := range contents {
		if content.Kind == domain.KnowledgeQAContentAnswer {
			for _, segment := range textsplit.Split(content.Content, domain.KnowledgeQAChunkLength, domain.KnowledgeQAChunkOverlap) {
				segment.Position = len(segments) + 1
				segments = append(segments, segment)
			}
			continue
		}
		segments = append(segments, textsplit.Segment{Position: len(segments) + 1, Content: content.Content, CharacterCount: utf8.RuneCountInString(content.Content)})
	}
	if len(segments) == 0 {
		return &ProcessError{Code: "empty_content", Stage: domain.KnowledgeIndexSplitting}
	}

	published, err := embedAndPublish(ctx, a.db, a.embedder, indexPublication{
		Model:                    (*servermodels.KnowledgeQAEntry)(nil),
		Batch:                    segmentBatch{OrganizationID: input.OrganizationID, KnowledgeBaseID: input.KnowledgeBaseID, SourceType: domain.KnowledgeSourceQAEntry, SourceID: input.EntryID, BatchID: input.ProcessingID, EmbeddingDimension: input.EmbeddingDimension},
		EmbeddingProviderID:      input.EmbeddingProviderID,
		EmbeddingModelIdentifier: input.EmbeddingModelIdentifier,
	}, segments)
	if err == nil && published {
		slog.Info("知识问答分段与向量完成", "entry_id", input.EntryID, "processing_id", input.ProcessingID, "segment_count", len(segments), "embedding_dimension", input.EmbeddingDimension, "duration_ms", time.Since(started).Milliseconds())
	}
	return err
}

// setStage 更新当前问答任务的执行阶段，任务已被替代或已进入终态时返回 false。
func (a *ProcessQAEntryAction) setStage(ctx context.Context, input ProcessQAInput, stage domain.KnowledgeIndexStatus) (bool, error) {
	return updateIndexStage(ctx, a.db, (*servermodels.KnowledgeQAEntry)(nil), input.EntryID, input.ProcessingID, stage)
}

// FinalizeFailure 保存当前问答任务的失败状态和原因码。
func (a *ProcessQAEntryAction) FinalizeFailure(ctx context.Context, input ProcessQAInput, runErr error) error {
	code, stage := indexFailureCode(runErr)
	changed, err := finalizeIndexFailure(ctx, a.db, (*servermodels.KnowledgeQAEntry)(nil), input.EntryID, input.ProcessingID, code)
	if err == nil && changed {
		slog.Warn("知识问答索引失败", "entry_id", input.EntryID, "processing_id", input.ProcessingID, "failure_code", code, "stage", stage)
	}
	return err
}
