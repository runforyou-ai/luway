//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/textsplit"
	"github.com/uptrace/bun"
)

// qaProcessInput 按条目当前的处理编号和知识库向量配置构造索引任务载荷。
func qaProcessInput(t *testing.T, db *bun.DB, organizationID, baseID, entryID string) (knowledgeaction.ProcessQAInput, servermodels.KnowledgeQAEntry) {
	t.Helper()
	var entry servermodels.KnowledgeQAEntry
	if err := db.NewSelect().Model(&entry).Where("kqe.id = ?", entryID).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := &servermodels.KnowledgeBase{ID: baseID}
	if err := db.NewSelect().Model(base).WherePK().Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return knowledgeaction.ProcessQAInput{
		OrganizationID: organizationID, KnowledgeBaseID: baseID, EntryID: entryID, ProcessingID: entry.ProcessingID,
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
	if err != nil {
		t.Fatal(err)
	}
	return segments
}

// TestKnowledgeQAIndexLifecycle 验证问答保存投递、分段构成、内容变更替换批次、重复保存保留批次、失败保留旧批次以及删除清理。
func TestKnowledgeQAIndexLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	identity, base := newQAFixture(t, db)
	tasks := newKnowledgeTasks(t, db)
	save := knowledgeaction.NewSaveQAEntryAction(db, tasks)
	probe := &processingProbe{}
	worker := knowledgeaction.NewProcessQAEntryAction(db, probe)
	answer := strings.Repeat("进入订单详情，点击申请退款，审核通过后原路退回。", 40)
	created, err := save.Execute(ctx, identity, base.ID, "", knowledgeaction.QAInput{Question: "如何退款？", Answer: answer, SimilarQuestions: []knowledgeaction.QASimilarQuestion{{Content: "退款入口"}, {Content: "怎么申请退款"}}})
	if err != nil {
		t.Fatal(err)
	}
	input, entry := qaProcessInput(t, db, identity.Organization.ID, base.ID, created.ID)
	if entry.Status != domain.KnowledgeIndexQueued || entry.ProcessingID == "" || input.EmbeddingModelID != base.EmbeddingModelID || input.EmbeddingDimension != 1024 {
		t.Fatalf("entry=%+v", entry)
	}
	count, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).Where("action_name = ? AND payload->>'entryId' = ?", knowledgeaction.ProcessQAEntryActionName, created.ID).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("tasks=%d %v", count, err)
	}
	if err := worker.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	// 主问题、两条相似问题各一段，答案按固定长度切段并顺延序号。
	answerSegments := textsplit.Split(answer, domain.KnowledgeQAChunkLength, domain.KnowledgeQAChunkOverlap)
	if len(answerSegments) < 2 {
		t.Fatalf("答案应切出多段，实际 %d", len(answerSegments))
	}
	_, entry = qaProcessInput(t, db, identity.Organization.ID, base.ID, created.ID)
	segments := qaSegments(t, db, entry)
	if entry.Status != domain.KnowledgeIndexSucceeded || entry.SegmentBatchID != input.ProcessingID || entry.SegmentCount != 3+len(answerSegments) || len(segments) != entry.SegmentCount {
		t.Fatalf("entry=%+v segments=%d", entry, len(segments))
	}
	if segments[0].Content != "如何退款？" || segments[0].CharacterCount != 5 || segments[1].Content != "退款入口" || segments[2].Content != "怎么申请退款" || segments[3].Content != answerSegments[0].Content {
		t.Fatalf("segments=%+v", segments)
	}
	for index, segment := range segments {
		if segment.Position != index+1 {
			t.Fatalf("position=%+v", segment)
		}
	}
	if probe.credential.APIKey != "test-key" {
		t.Fatalf("credential=%+v", probe.credential)
	}
	stored, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND embedding_dimension = 1024 AND embedding IS NOT NULL AND search_vector IS NOT NULL", created.ID).Count(ctx)
	if err != nil || stored != entry.SegmentCount {
		t.Fatalf("stored=%d %v", stored, err)
	}

	// 内容相同的重复保存保留已发布批次和任务编号。
	unchanged := knowledgeaction.QAInput{Question: created.Question, Answer: created.Answer, SimilarQuestions: created.SimilarQuestions}
	if _, err := save.Execute(ctx, identity, base.ID, created.ID, unchanged); err != nil {
		t.Fatal(err)
	}
	_, entry = qaProcessInput(t, db, identity.Organization.ID, base.ID, created.ID)
	if entry.ProcessingID != input.ProcessingID || entry.Status != domain.KnowledgeIndexSucceeded {
		t.Fatalf("unchanged entry=%+v", entry)
	}

	// 修改相似问题后投递新任务，旧任务在阶段更新时退出，新批次替换旧分段。
	unchanged.SimilarQuestions = []knowledgeaction.QASimilarQuestion{created.SimilarQuestions[0]}
	if _, err := save.Execute(ctx, identity, base.ID, created.ID, unchanged); err != nil {
		t.Fatal(err)
	}
	next, entry := qaProcessInput(t, db, identity.Organization.ID, base.ID, created.ID)
	if next.ProcessingID == input.ProcessingID || entry.Status != domain.KnowledgeIndexQueued || entry.SegmentBatchID != input.ProcessingID {
		t.Fatalf("edited entry=%+v", entry)
	}
	if err := worker.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := worker.FinalizeFailure(ctx, input, errors.New("old failure")); err != nil {
		t.Fatal(err)
	}
	_, entry = qaProcessInput(t, db, identity.Organization.ID, base.ID, created.ID)
	if entry.Status != domain.KnowledgeIndexQueued || entry.SegmentBatchID != input.ProcessingID {
		t.Fatalf("old task changed entry=%+v", entry)
	}
	if err := worker.Execute(ctx, next); err != nil {
		t.Fatal(err)
	}
	_, entry = qaProcessInput(t, db, identity.Organization.ID, base.ID, created.ID)
	segments = qaSegments(t, db, entry)
	if entry.SegmentBatchID != next.ProcessingID || entry.SegmentCount != 2+len(answerSegments) || len(segments) != entry.SegmentCount || segments[1].Content != "退款入口" {
		t.Fatalf("replaced entry=%+v segments=%+v", entry, segments)
	}
	count, err = db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND segment_batch_id = ?", created.ID, input.ProcessingID).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("old batch=%d %v", count, err)
	}

	// 向量接口失败时记录失败状态并保留上一批次；重试后再次成功。
	retry := knowledgeaction.NewQAProcessing(db, tasks)
	if err := retry.Retry(ctx, identity, base.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	failing, entry := qaProcessInput(t, db, identity.Organization.ID, base.ID, created.ID)
	if failing.ProcessingID == next.ProcessingID || entry.Status != domain.KnowledgeIndexQueued {
		t.Fatalf("retry entry=%+v", entry)
	}
	probe.embedFail = true
	runErr := worker.Execute(ctx, failing)
	var failure *knowledgeaction.ProcessError
	if !errors.As(runErr, &failure) || failure.Code != "embedding_failed" || failure.Stage != domain.KnowledgeIndexEmbedding {
		t.Fatalf("failure=%v", runErr)
	}
	if err := worker.FinalizeFailure(ctx, failing, runErr); err != nil {
		t.Fatal(err)
	}
	_, entry = qaProcessInput(t, db, identity.Organization.ID, base.ID, created.ID)
	if entry.Status != domain.KnowledgeIndexFailed || entry.FailureCode != "embedding_failed" || entry.SegmentBatchID != next.ProcessingID || len(qaSegments(t, db, entry)) != entry.SegmentCount {
		t.Fatalf("failed entry=%+v", entry)
	}
	page, err := knowledgeaction.NewListQAEntriesQuery(db).Execute(ctx, identity, base.ID, knowledgeaction.QAListInput{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Status != domain.KnowledgeIndexFailed || page.Entries[0].FailureCode != "embedding_failed" {
		t.Fatalf("list=%+v err=%v", page, err)
	}
	probe.embedFail = false
	// 保存未完成索引的条目时即使内容未变也会重新投递。
	if _, err := save.Execute(ctx, identity, base.ID, created.ID, unchanged); err != nil {
		t.Fatal(err)
	}
	again, entry := qaProcessInput(t, db, identity.Organization.ID, base.ID, created.ID)
	if again.ProcessingID == failing.ProcessingID || entry.Status != domain.KnowledgeIndexQueued {
		t.Fatalf("resaved entry=%+v", entry)
	}
	if err := worker.Execute(ctx, again); err != nil {
		t.Fatal(err)
	}

	// 企业隔离与删除清理。
	foreign, _ := newQAFixture(t, db)
	if err := retry.Retry(ctx, foreign, base.ID, created.ID); !errors.Is(err, knowledgeaction.ErrNotFound) {
		t.Fatalf("foreign retry=%v", err)
	}
	if err := knowledgeaction.NewDeleteQAEntryAction(db).Execute(ctx, identity, base.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	count, err = db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ?", created.ID).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("remaining=%d %v", count, err)
	}
	if err := worker.Execute(ctx, again); err != nil {
		t.Fatal(err)
	}
	another, err := save.Execute(ctx, identity, base.ID, "", knowledgeaction.QAInput{Question: "发票怎么开", Answer: "下单时选择电子发票。"})
	if err != nil {
		t.Fatal(err)
	}
	if input, _ := qaProcessInput(t, db, identity.Organization.ID, base.ID, another.ID); worker.Execute(ctx, input) != nil {
		t.Fatal(err)
	}
	if err := knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, base.ID); err != nil {
		t.Fatal(err)
	}
	count, err = db.NewSelect().TableExpr("public.knowledge_segments").Where("knowledge_base_id = ?", base.ID).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("base remaining=%d %v", count, err)
	}
}
