//go:build server

package integrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestKnowledgeBaseReindex 验证索引参数变更清空全部分段并按新配置投递，召回参数变更保留已发布批次，被替代的任务不再发布。
func TestKnowledgeBaseReindex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	identity := installed.Identity
	tasks := newKnowledgeTasks(t, db)
	probe := &processingProbe{}
	documentWorker := knowledgeaction.NewProcessDocumentAction(db, probe, modelcall.New(db, modelcall.Upstreams{Embedder: probe}, nil), probe, probe)
	qaWorker := knowledgeaction.NewProcessQAEntryAction(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe}, nil))
	update := knowledgeaction.NewUpdateKnowledgeBaseAction(db, tasks)
	segmentCount := func(baseID string) int {
		count, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("knowledge_base_id = ?", baseID).Count(ctx)
		require.NoError(t, err)
		return int(count)
	}
	// 按文档当前处理编号、分段参数和知识库向量配置构造处理载荷。
	documentInput := func(documentID string) (knowledgeaction.ProcessInput, servermodels.KnowledgeDocument) {
		var document servermodels.KnowledgeDocument
		require.NoError(t, db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx))
		return knowledgeaction.ProcessInput{WorkspaceID: identity.Workspace.ID, KnowledgeBaseID: base.ID, DocumentID: documentID, ProcessingID: document.ProcessingID, ChunkLength: document.ChunkLength, ChunkOverlap: document.ChunkOverlap, EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension}, document
	}

	file := uploadedDocumentFile(t, db, identity, "资料.txt")
	documents, err := knowledgeaction.NewCreateDocumentsAction(db, tasks).Execute(ctx, identity, base.ID, []string{file.ID})
	require.NoError(t, err)
	documentID := documents[0].ID
	published, _ := documentInput(documentID)
	require.NoError(t, documentWorker.Execute(ctx, published))
	qaBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "FAQ", domain.KnowledgeBaseCategoryQA))
	require.NoError(t, err)
	entry, err := knowledgeaction.NewSaveQAEntryAction(db, tasks).Execute(ctx, identity, qaBase.ID, "", knowledgeaction.QAInput{Question: "如何退款？", Answer: "进入订单详情申请退款。"})
	require.NoError(t, err)
	qaInput, _ := qaProcessInput(t, db, identity.Workspace.ID, qaBase.ID, entry.ID)
	require.NoError(t, qaWorker.Execute(ctx, qaInput))
	qaSegments := segmentCount(qaBase.ID)
	require.Equal(t, 1, segmentCount(base.ID))
	require.NotZero(t, qaSegments)

	// 召回数量和相关性阈值变更保留已发布批次。
	input := knowledgeaction.Input{Name: base.Name, Category: base.Category, EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension, ChunkLength: base.ChunkLength, ChunkOverlap: base.ChunkOverlap, RetrievalCount: 5, RetrievalScoreThreshold: 0.5, RerankModelID: base.RerankModelID}
	_, err = update.Execute(ctx, identity, base.ID, input)
	require.NoError(t, err)
	_, document := documentInput(documentID)
	require.Equal(t, published.ProcessingID, document.ProcessingID)
	require.Equal(t, published.ProcessingID, document.SegmentBatchID)
	require.Equal(t, 1, segmentCount(base.ID))

	// 分段长度变更清空本库分段并按新参数投递，其他知识库分段保留。
	input.ChunkLength = new(1024)
	_, err = update.Execute(ctx, identity, base.ID, input)
	require.NoError(t, err)
	reindexed, document := documentInput(documentID)
	require.Equal(t, domain.KnowledgeIndexQueued, document.Status)
	require.Empty(t, document.SegmentBatchID)
	require.Zero(t, document.SegmentCount)
	require.Equal(t, 1024, document.ChunkLength)
	require.NotEqual(t, published.ProcessingID, document.ProcessingID)
	require.Zero(t, segmentCount(base.ID))
	require.Equal(t, qaSegments, segmentCount(qaBase.ID))
	documentTasks := servertest.QueuedInputs(t, tasks, knowledgeaction.ProcessDocumentActionName, func(input knowledgeaction.ProcessInput) bool { return input.DocumentID == documentID })
	require.Len(t, documentTasks, 2)
	_, err = knowledgeaction.NewRetrievalService(db, modelcall.New(db, modelcall.Upstreams{Embedder: nil, Reranker: nil}, nil)).Retrieve(ctx, identity, base.ID, "正文")
	require.ErrorIs(t, err, knowledgeaction.ErrRetrievalNotReady)
	// 被替代的任务不再发布，新任务按新参数发布。
	require.NoError(t, documentWorker.Execute(ctx, published))
	require.Zero(t, segmentCount(base.ID))
	require.NoError(t, documentWorker.Execute(ctx, reindexed))
	_, document = documentInput(documentID)
	require.Equal(t, domain.KnowledgeIndexSucceeded, document.Status)
	require.Equal(t, reindexed.ProcessingID, document.SegmentBatchID)
	require.Equal(t, 1, segmentCount(base.ID))

	// 问答库更换为同供应商的另一个向量模型和维度后按新配置重新索引。
	qaEmbedding := loadTestAIModel(t, db, qaBase.EmbeddingModelID)
	embeddingB := aiModelID(t, db, qaEmbedding.ProviderID, "embedding-b")
	qaUpdate := knowledgeaction.Input{Name: qaBase.Name, Category: qaBase.Category, EmbeddingModelID: embeddingB, EmbeddingDimension: 768, RetrievalCount: qaBase.RetrievalCount, RetrievalScoreThreshold: qaBase.RetrievalScoreThreshold, RerankModelID: qaBase.RerankModelID}
	_, err = update.Execute(ctx, identity, qaBase.ID, qaUpdate)
	require.NoError(t, err)
	qaReindexed, stored := qaProcessInput(t, db, identity.Workspace.ID, qaBase.ID, entry.ID)
	require.Equal(t, domain.KnowledgeIndexQueued, stored.Status)
	require.Empty(t, stored.SegmentBatchID)
	require.Equal(t, embeddingB, qaReindexed.EmbeddingModelID)
	require.Equal(t, 768, qaReindexed.EmbeddingDimension)
	require.Zero(t, segmentCount(qaBase.ID))
	require.NoError(t, qaWorker.Execute(ctx, qaReindexed))
	dimensions, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("knowledge_base_id = ? AND embedding_dimension = 768", qaBase.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, qaSegments, int(dimensions))
}
