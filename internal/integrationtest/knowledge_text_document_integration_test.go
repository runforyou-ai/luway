//go:build server

package integrationtest

import (
	"context"
	"testing"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// loadKnowledgeDocument 读取当前文档行。
func loadKnowledgeDocument(t *testing.T, db *bun.DB, documentID string) *servermodels.KnowledgeDocument {
	t.Helper()
	document := &servermodels.KnowledgeDocument{}
	require.NoError(t, db.NewSelect().Model(document).Where("kd.id = ?", documentID).Scan(context.Background()))
	return document
}

// runDocumentProcessing 按文档当前的处理编号、分段参数和知识库向量配置执行一次处理，fetchPage 表示本次是否重新抓取网页。
func runDocumentProcessing(t *testing.T, db *bun.DB, probe *retrievalProbe, workspaceID, baseID, documentID string, fetchPage bool) {
	t.Helper()
	document := loadKnowledgeDocument(t, db, documentID)
	base := &servermodels.KnowledgeBase{ID: baseID}
	require.NoError(t, db.NewSelect().Model(base).WherePK().Scan(context.Background()))
	err := knowledgeaction.NewProcessDocumentAction(db, probe, modelcall.New(db, modelcall.Upstreams{Embedder: probe}, nil), probe, probe).Execute(context.Background(), knowledgeaction.ProcessInput{
		WorkspaceID: workspaceID, KnowledgeBaseID: baseID, DocumentID: documentID, SourceKind: document.SourceKind, FetchPage: fetchPage, ProcessingID: document.ProcessingID,
		ChunkLength: document.ChunkLength, ChunkOverlap: document.ChunkOverlap,
		EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension,
	})
	require.NoError(t, err)
}

// TestKnowledgeTextDocumentLifecycle 验证在线文档的创建、索引、改名、正文编辑、标题过滤与召回名称、来源限制与删除清理。
func TestKnowledgeTextDocumentLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	identity := installed.Identity
	probe := &retrievalProbe{}
	save := knowledgeaction.NewSaveTextDocumentAction(db, newKnowledgeTasks(t, db))

	created, err := save.Execute(ctx, identity, base.ID, "", knowledgeaction.TextDocumentInput{Title: "退款说明", Content: "# 退款\n\n签收后七天内可以申请退款。"})
	require.NoError(t, err)
	require.Equal(t, "退款说明", created.Name)
	require.Equal(t, domain.KnowledgeDocumentSourceText, created.SourceKind)
	document := loadKnowledgeDocument(t, db, created.ID)
	require.Equal(t, domain.KnowledgeIndexQueued, document.Status)
	require.NotEmpty(t, document.ProcessingID)
	require.Empty(t, document.FileID)

	runDocumentProcessing(t, db, probe, identity.Workspace.ID, base.ID, created.ID, false)
	document = loadKnowledgeDocument(t, db, created.ID)
	require.Equal(t, domain.KnowledgeIndexSucceeded, document.Status)
	require.Equal(t, document.ProcessingID, document.SegmentBatchID)
	require.NotZero(t, document.SegmentCount)
	published := document.ProcessingID

	// 只改名称保留已发布批次，不投递新任务。
	renamed, err := knowledgeaction.NewRenameDocumentAction(db).Execute(ctx, identity, base.ID, created.ID, "退款政策说明")
	require.NoError(t, err)
	require.Equal(t, "退款政策说明", renamed.Name)
	document = loadKnowledgeDocument(t, db, created.ID)
	require.Equal(t, published, document.ProcessingID)
	require.Equal(t, domain.KnowledgeIndexSucceeded, document.Status)

	// 正文未变化时同样不投递新任务。
	_, err = save.Execute(ctx, identity, base.ID, created.ID, knowledgeaction.TextDocumentInput{Title: "退款政策说明", Content: "# 退款\n\n签收后七天内可以申请退款。"})
	require.NoError(t, err)
	document = loadKnowledgeDocument(t, db, created.ID)
	require.Equal(t, published, document.ProcessingID)

	// 正文变化后替换批次，旧分段被清除。
	_, err = save.Execute(ctx, identity, base.ID, created.ID, knowledgeaction.TextDocumentInput{Title: "退款政策说明", Content: "# 退款\n\n签收后七天内可以申请退款，退款金额原路返回。"})
	require.NoError(t, err)
	document = loadKnowledgeDocument(t, db, created.ID)
	require.NotEqual(t, published, document.ProcessingID)
	require.Equal(t, domain.KnowledgeIndexQueued, document.Status)
	require.Equal(t, published, document.SegmentBatchID)
	runDocumentProcessing(t, db, probe, identity.Workspace.ID, base.ID, created.ID, false)
	document = loadKnowledgeDocument(t, db, created.ID)
	require.Equal(t, document.ProcessingID, document.SegmentBatchID)
	require.Equal(t, domain.KnowledgeIndexSucceeded, document.Status)
	stale, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND segment_batch_id = ?", created.ID, published).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, stale)
	content, err := knowledgeaction.NewDocumentQuery(db).Content(ctx, identity, base.ID, created.ID)
	require.NoError(t, err)
	require.Equal(t, "# 退款\n\n签收后七天内可以申请退款，退款金额原路返回。", content.Content)

	// 上传原件来源不支持正文读取、正文编辑与改名。
	uploaded := publishRetrievalDocument(t, db, probe, identity, base, "配送说明.txt", "配送时效按收货地址计算。")
	_, err = knowledgeaction.NewDocumentQuery(db).Content(ctx, identity, base.ID, uploaded)
	require.ErrorIs(t, err, knowledgeaction.ErrDocumentSourceUnsupported)
	_, err = save.Execute(ctx, identity, base.ID, uploaded, knowledgeaction.TextDocumentInput{Title: "配送说明", Content: "正文"})
	require.ErrorIs(t, err, knowledgeaction.ErrDocumentSourceUnsupported)
	_, err = knowledgeaction.NewRenameDocumentAction(db).Execute(ctx, identity, base.ID, uploaded, "配送政策")
	require.ErrorIs(t, err, knowledgeaction.ErrDocumentSourceUnsupported)

	// 列表按文档标题过滤，在线文档的类型与大小取自正文。
	titled, err := knowledgeaction.NewDocumentQuery(db).List(ctx, identity, base.ID, knowledgeaction.DocumentListInput{Keyword: "退款政策"})
	require.NoError(t, err)
	require.Len(t, titled.Documents, 1)
	require.Equal(t, created.ID, titled.Documents[0].ID)
	require.Equal(t, domain.KnowledgeDocumentMarkdownContentType, titled.Documents[0].ContentType)
	require.Equal(t, int64(len(content.Content)), titled.Documents[0].ByteSize)

	// 召回结果的来源名称取文档标题。
	records, err := knowledgeaction.NewRetrievalService(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe, Reranker: probe}, nil)).Retrieve(ctx, identity, base.ID, "如何申请退款")
	require.NoError(t, err)
	require.NotEmpty(t, records)
	require.Equal(t, created.ID, records[0].DocumentID)
	require.Equal(t, "退款政策说明", records[0].DocumentName)

	// 删除文档时正文与分段一并清除。
	require.NoError(t, knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, identity, base.ID, created.ID))
	contents, err := db.NewSelect().Model((*servermodels.KnowledgeDocumentContent)(nil)).Where("document_id = ?", created.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, contents)
	segments, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ?", created.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, segments)
}
