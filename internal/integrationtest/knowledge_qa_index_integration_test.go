//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/mdchunk/split"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// qaProcessInput 按条目当前的处理编号和知识库向量配置构造索引任务载荷。
func qaProcessInput(t *testing.T, db *bun.DB, workspaceID, baseID, entryID string) (knowledgeaction.ProcessQAInput, servermodels.KnowledgeQAEntry) {
	t.Helper()
	var entry servermodels.KnowledgeQAEntry
	require.NoError(t, db.NewSelect().Model(&entry).Where("kqe.id = ?", entryID).Scan(context.Background()))
	base := &servermodels.KnowledgeBase{ID: baseID}
	require.NoError(t, db.NewSelect().Model(base).WherePK().Scan(context.Background()))
	return knowledgeaction.ProcessQAInput{
		WorkspaceID: workspaceID, KnowledgeBaseID: baseID, EntryID: entryID, ProcessingID: entry.ProcessingID,
		EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension,
	}, entry
}

// qaSegments 读取条目当前已发布批次的分段正文与序号。
func qaSegments(t *testing.T, db *bun.DB, entry servermodels.KnowledgeQAEntry) []knowledgeaction.Segment {
	t.Helper()
	segments := make([]knowledgeaction.Segment, 0)
	err := db.NewSelect().TableExpr("public.knowledge_segments").ColumnExpr("id, position, content, character_count").
		Where("source_type = ? AND source_id = ? AND segment_batch_id = ?", domain.KnowledgeSourceQAEntry, entry.ID, entry.SegmentBatchID).
		OrderExpr("position").Scan(context.Background(), &segments)
	require.NoError(t, err)
	return segments
}

// TestKnowledgeQAIndexLifecycle 验证问答保存投递、分段构成、内容变更替换批次、重复保存保留批次、失败保留旧批次以及删除清理。
func TestKnowledgeQAIndexLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	identity, base := newQAFixture(t, db)
	tasks := newKnowledgeTasks(t, db)
	save := knowledgeaction.NewSaveQAEntryAction(db, tasks)
	probe := &processingProbe{}
	worker := knowledgeaction.NewProcessQAEntryAction(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe}, nil))
	answer := strings.Repeat("进入订单详情，点击申请退款，审核通过后原路退回。", 40)
	created, err := save.Execute(ctx, identity, base.ID, "", knowledgeaction.QAInput{Question: "如何退款？", Answer: answer, SimilarQuestions: []knowledgeaction.QASimilarQuestion{{Content: "退款入口"}, {Content: "怎么申请退款"}}})
	require.NoError(t, err)
	input, entry := qaProcessInput(t, db, identity.Workspace.ID, base.ID, created.ID)
	require.Equal(t, domain.KnowledgeIndexQueued, entry.Status)
	require.NotEmpty(t, entry.ProcessingID)
	require.Equal(t, base.EmbeddingModelID, input.EmbeddingModelID)
	require.Equal(t, 1024, input.EmbeddingDimension)
	entryTasks := servertest.QueuedInputs(t, tasks, knowledgeaction.ProcessQAEntryActionName, func(input knowledgeaction.ProcessQAInput) bool { return input.EntryID == created.ID })
	require.Len(t, entryTasks, 1)
	require.NoError(t, worker.Execute(ctx, input))
	// 主问题、两条相似问题各一段，答案按固定长度切段并顺延序号。
	answerSplitter, err := split.New(split.Options{Size: domain.KnowledgeQAChunkLength, Overlap: domain.KnowledgeQAChunkOverlap})
	require.NoError(t, err)
	answerSegments := answerSplitter.Split(answer)
	require.GreaterOrEqual(t, len(answerSegments), 2, "答案应切出多段")
	_, entry = qaProcessInput(t, db, identity.Workspace.ID, base.ID, created.ID)
	segments := qaSegments(t, db, entry)
	require.Equal(t, domain.KnowledgeIndexSucceeded, entry.Status)
	require.Equal(t, input.ProcessingID, entry.SegmentBatchID)
	require.Equal(t, 3+len(answerSegments), entry.SegmentCount)
	require.Len(t, segments, entry.SegmentCount)
	require.Equal(t, struct {
		Question                   string
		QuestionCount              int
		Similar1, Similar2, Answer string
	}{"如何退款？", 5, "退款入口", "怎么申请退款", answerSegments[0].Text}, struct {
		Question                   string
		QuestionCount              int
		Similar1, Similar2, Answer string
	}{segments[0].Content, segments[0].CharacterCount, segments[1].Content, segments[2].Content, segments[3].Content})
	for index, segment := range segments {
		require.Equal(t, index+1, segment.Position, "position=%+v", segment)
	}
	require.Equal(t, "test-key", probe.credential.APIKey)
	stored, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND embedding_dimension = 1024 AND embedding IS NOT NULL AND search_vector IS NOT NULL", created.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, entry.SegmentCount, int(stored))

	// 内容相同的重复保存保留已发布批次和任务编号。
	unchanged := knowledgeaction.QAInput{Question: created.Question, Answer: created.Answer, SimilarQuestions: created.SimilarQuestions}
	_, err = save.Execute(ctx, identity, base.ID, created.ID, unchanged)
	require.NoError(t, err)
	_, entry = qaProcessInput(t, db, identity.Workspace.ID, base.ID, created.ID)
	require.Equal(t, input.ProcessingID, entry.ProcessingID)
	require.Equal(t, domain.KnowledgeIndexSucceeded, entry.Status)

	// 修改相似问题后投递新任务，旧任务在阶段更新时退出，新批次替换旧分段。
	unchanged.SimilarQuestions = []knowledgeaction.QASimilarQuestion{created.SimilarQuestions[0]}
	_, err = save.Execute(ctx, identity, base.ID, created.ID, unchanged)
	require.NoError(t, err)
	next, entry := qaProcessInput(t, db, identity.Workspace.ID, base.ID, created.ID)
	require.NotEqual(t, input.ProcessingID, next.ProcessingID)
	require.Equal(t, domain.KnowledgeIndexQueued, entry.Status)
	require.Equal(t, input.ProcessingID, entry.SegmentBatchID)
	require.NoError(t, worker.Execute(ctx, input))
	require.NoError(t, worker.FinalizeFailure(ctx, input, errors.New("old failure")))
	_, entry = qaProcessInput(t, db, identity.Workspace.ID, base.ID, created.ID)
	require.Equal(t, domain.KnowledgeIndexQueued, entry.Status, "old task changed entry")
	require.Equal(t, input.ProcessingID, entry.SegmentBatchID, "old task changed entry")
	require.NoError(t, worker.Execute(ctx, next))
	_, entry = qaProcessInput(t, db, identity.Workspace.ID, base.ID, created.ID)
	segments = qaSegments(t, db, entry)
	require.Equal(t, next.ProcessingID, entry.SegmentBatchID)
	require.Equal(t, 2+len(answerSegments), entry.SegmentCount)
	require.Len(t, segments, entry.SegmentCount)
	require.Equal(t, "退款入口", segments[1].Content)
	count, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND segment_batch_id = ?", created.ID, input.ProcessingID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "old batch")

	// 向量接口失败时记录失败状态并保留上一批次；重试后再次成功。
	retry := knowledgeaction.NewQAProcessing(db, tasks)
	require.NoError(t, retry.Retry(ctx, identity, base.ID, created.ID))
	failing, entry := qaProcessInput(t, db, identity.Workspace.ID, base.ID, created.ID)
	require.NotEqual(t, next.ProcessingID, failing.ProcessingID)
	require.Equal(t, domain.KnowledgeIndexQueued, entry.Status)
	probe.embedFail = true
	runErr := worker.Execute(ctx, failing)
	var failure *knowledgeaction.ProcessError
	require.ErrorAs(t, runErr, &failure)
	require.Equal(t, "embedding_failed", failure.Code)
	require.Equal(t, domain.KnowledgeIndexEmbedding, failure.Stage)
	require.NoError(t, worker.FinalizeFailure(ctx, failing, runErr))
	_, entry = qaProcessInput(t, db, identity.Workspace.ID, base.ID, created.ID)
	require.Equal(t, domain.KnowledgeIndexFailed, entry.Status)
	require.Equal(t, "embedding_failed", entry.FailureCode)
	require.Equal(t, next.ProcessingID, entry.SegmentBatchID)
	require.Len(t, qaSegments(t, db, entry), entry.SegmentCount)
	page, err := knowledgeaction.NewListQAEntriesQuery(db).Execute(ctx, identity, base.ID, knowledgeaction.QAListInput{})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	require.Equal(t, domain.KnowledgeIndexFailed, page.Entries[0].Status)
	require.Equal(t, "embedding_failed", page.Entries[0].FailureCode)
	probe.embedFail = false
	// 保存未完成索引的条目时即使内容未变也会重新投递。
	_, err = save.Execute(ctx, identity, base.ID, created.ID, unchanged)
	require.NoError(t, err)
	again, entry := qaProcessInput(t, db, identity.Workspace.ID, base.ID, created.ID)
	require.NotEqual(t, failing.ProcessingID, again.ProcessingID)
	require.Equal(t, domain.KnowledgeIndexQueued, entry.Status)
	require.NoError(t, worker.Execute(ctx, again))

	// 企业隔离与删除清理。
	foreign, _ := newQAFixture(t, db)
	require.ErrorIs(t, retry.Retry(ctx, foreign, base.ID, created.ID), knowledgeaction.ErrNotFound, "foreign retry")
	require.NoError(t, knowledgeaction.NewDeleteQAEntryAction(db).Execute(ctx, identity, base.ID, created.ID))
	count, err = db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ?", created.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "remaining")
	require.NoError(t, worker.Execute(ctx, again))
	another, err := save.Execute(ctx, identity, base.ID, "", knowledgeaction.QAInput{Question: "发票怎么开", Answer: "下单时选择电子发票。"})
	require.NoError(t, err)
	anotherInput, _ := qaProcessInput(t, db, identity.Workspace.ID, base.ID, another.ID)
	require.NoError(t, worker.Execute(ctx, anotherInput))
	require.NoError(t, knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, base.ID))
	count, err = db.NewSelect().TableExpr("public.knowledge_segments").Where("knowledge_base_id = ?", base.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "base remaining")
}
