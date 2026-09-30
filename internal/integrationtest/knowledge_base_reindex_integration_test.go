//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestKnowledgeBaseReindex 验证索引参数变更清空全部分段并按新配置投递，召回参数变更保留已发布批次，被替代的任务不再发布。
func TestKnowledgeBaseReindex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	identity := installed.Identity
	tasks := newKnowledgeTasks(t, db)
	probe := &processingProbe{}
	documentWorker := knowledgeaction.NewProcessDocumentAction(db, probe, probe, probe, probe)
	qaWorker := knowledgeaction.NewProcessQAEntryAction(db, probe)
	update := knowledgeaction.NewUpdateKnowledgeBaseAction(db, tasks)
	segmentCount := func(baseID string) int {
		count, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("knowledge_base_id = ?", baseID).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	// 按文档当前任务快照构造处理载荷。
	documentInput := func(documentID string) (knowledgeaction.ProcessInput, servermodels.KnowledgeDocument) {
		var document servermodels.KnowledgeDocument
		if err := db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		return knowledgeaction.ProcessInput{OrganizationID: identity.Organization.ID, KnowledgeBaseID: base.ID, DocumentID: documentID, ProcessingID: document.ProcessingID, ChunkLength: document.ChunkLength, ChunkOverlap: document.ChunkOverlap, EmbeddingProviderID: document.EmbeddingProviderID, EmbeddingModelIdentifier: document.EmbeddingModelIdentifier, EmbeddingDimension: document.EmbeddingDimension}, document
	}

	file := uploadedDocumentFile(t, db, identity, "资料.txt")
	documents, err := knowledgeaction.NewCreateDocumentsAction(db, tasks).Execute(ctx, identity, base.ID, []string{file.ID})
	if err != nil {
		t.Fatal(err)
	}
	documentID := documents[0].ID
	published, _ := documentInput(documentID)
	if err := documentWorker.Execute(ctx, published); err != nil {
		t.Fatal(err)
	}
	qaBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "FAQ", domain.KnowledgeBaseCategoryQA))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := knowledgeaction.NewSaveQAEntryAction(db, tasks).Execute(ctx, identity, qaBase.ID, "", knowledgeaction.QAInput{Question: "如何退款？", Answer: "进入订单详情申请退款。"})
	if err != nil {
		t.Fatal(err)
	}
	qaInput, _ := qaProcessInput(t, db, identity.Organization.ID, qaBase.ID, entry.ID)
	if err := qaWorker.Execute(ctx, qaInput); err != nil {
		t.Fatal(err)
	}
	qaSegments := segmentCount(qaBase.ID)
	if segmentCount(base.ID) != 1 || qaSegments == 0 {
		t.Fatalf("segments=%d qa=%d", segmentCount(base.ID), qaSegments)
	}

	// 召回数量和相关性阈值变更保留已发布批次。
	input := knowledgeaction.Input{Name: base.Name, Category: base.Category, EmbeddingProviderID: base.EmbeddingProviderID, EmbeddingModelIdentifier: base.EmbeddingModelIdentifier, EmbeddingDimension: base.EmbeddingDimension, ChunkLength: base.ChunkLength, ChunkOverlap: base.ChunkOverlap, RetrievalCount: 5, RetrievalScoreThreshold: 0.5, RerankProviderID: base.RerankProviderID, RerankModelIdentifier: base.RerankModelIdentifier}
	if _, err := update.Execute(ctx, identity, base.ID, input); err != nil {
		t.Fatal(err)
	}
	if _, document := documentInput(documentID); document.ProcessingID != published.ProcessingID || document.SegmentBatchID != published.ProcessingID || segmentCount(base.ID) != 1 {
		t.Fatalf("document=%+v", document)
	}

	// 分段长度变更清空本库分段并按新参数投递，其他知识库分段保留。
	length := 1024
	input.ChunkLength = &length
	if _, err := update.Execute(ctx, identity, base.ID, input); err != nil {
		t.Fatal(err)
	}
	reindexed, document := documentInput(documentID)
	if document.Status != domain.KnowledgeIndexQueued || document.SegmentBatchID != "" || document.SegmentCount != 0 || document.ChunkLength != 1024 || document.ProcessingID == published.ProcessingID {
		t.Fatalf("document=%+v", document)
	}
	if segmentCount(base.ID) != 0 || segmentCount(qaBase.ID) != qaSegments {
		t.Fatalf("segments=%d qa=%d", segmentCount(base.ID), segmentCount(qaBase.ID))
	}
	count, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).Where("action_name = ? AND payload->>'documentId' = ?", knowledgeaction.ProcessDocumentActionName, documentID).Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("tasks=%d %v", count, err)
	}
	if _, err := knowledgeaction.NewRetrievalService(db, nil, nil).Retrieve(ctx, identity, base.ID, "正文"); !errors.Is(err, knowledgeaction.ErrRetrievalNotReady) {
		t.Fatalf("retrieve err=%v", err)
	}
	// 被替代的任务不再发布，新任务按新参数发布。
	if err := documentWorker.Execute(ctx, published); err != nil || segmentCount(base.ID) != 0 {
		t.Fatalf("stale publish segments=%d err=%v", segmentCount(base.ID), err)
	}
	if err := documentWorker.Execute(ctx, reindexed); err != nil {
		t.Fatal(err)
	}
	if _, document := documentInput(documentID); document.Status != domain.KnowledgeIndexSucceeded || document.SegmentBatchID != reindexed.ProcessingID || segmentCount(base.ID) != 1 {
		t.Fatalf("document=%+v", document)
	}

	// 问答库更换向量模型和维度后按新快照重新索引。
	qaUpdate := knowledgeaction.Input{Name: qaBase.Name, Category: qaBase.Category, EmbeddingProviderID: qaBase.EmbeddingProviderID, EmbeddingModelIdentifier: "embedding-b", EmbeddingDimension: 768, RetrievalCount: qaBase.RetrievalCount, RetrievalScoreThreshold: qaBase.RetrievalScoreThreshold, RerankProviderID: qaBase.RerankProviderID, RerankModelIdentifier: qaBase.RerankModelIdentifier}
	if _, err := update.Execute(ctx, identity, qaBase.ID, qaUpdate); err != nil {
		t.Fatal(err)
	}
	qaReindexed, stored := qaProcessInput(t, db, identity.Organization.ID, qaBase.ID, entry.ID)
	if stored.Status != domain.KnowledgeIndexQueued || stored.SegmentBatchID != "" || stored.EmbeddingModelIdentifier != "embedding-b" || stored.EmbeddingDimension != 768 || segmentCount(qaBase.ID) != 0 {
		t.Fatalf("entry=%+v segments=%d", stored, segmentCount(qaBase.ID))
	}
	if err := qaWorker.Execute(ctx, qaReindexed); err != nil {
		t.Fatal(err)
	}
	dimensions, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("knowledge_base_id = ? AND embedding_dimension = 768", qaBase.ID).Count(ctx)
	if err != nil || dimensions != qaSegments {
		t.Fatalf("dimensions=%d %v", dimensions, err)
	}
}
