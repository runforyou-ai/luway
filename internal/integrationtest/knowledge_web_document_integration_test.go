//go:build server

package integrationtest

import (
	"context"
	"testing"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/stretchr/testify/require"
)

// TestKnowledgeWebDocumentLifecycle 验证网页导入的抓取、快照、重试不出网、重新抓取与失败保留。
func TestKnowledgeWebDocumentLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	identity := installed.Identity
	tasks := newKnowledgeTasks(t, db)
	probe := &retrievalProbe{}
	create := knowledgeaction.NewCreateWebDocumentAction(db, tasks)
	processing := knowledgeaction.NewDocumentProcessing(db, tasks)

	// 非 http 与 https 的地址在保存期拒绝。
	_, err = create.Execute(ctx, identity, base.ID, knowledgeaction.WebDocumentInput{Title: "帮助中心", SourceURL: "ftp://example.com/help"})
	require.ErrorAs(t, err, new(*common.FieldError))

	created, err := create.Execute(ctx, identity, base.ID, knowledgeaction.WebDocumentInput{Title: "帮助中心", SourceURL: "https://example.com/help#section"})
	require.NoError(t, err)
	require.Equal(t, domain.KnowledgeDocumentSourceWeb, created.SourceKind)
	require.Equal(t, "https://example.com/help", created.SourceURL)
	// 同一知识库中重复导入同一页面被拒绝。
	_, err = create.Execute(ctx, identity, base.ID, knowledgeaction.WebDocumentInput{Title: "帮助中心副本", SourceURL: "https://example.com/help"})
	require.ErrorIs(t, err, knowledgeaction.ErrDocumentURLDuplicate)

	probe.markdown = "# 帮助\n\n签收后七天内可以申请退款。"
	runDocumentProcessing(t, db, probe, identity.Workspace.ID, base.ID, created.ID, true)
	document := loadKnowledgeDocument(t, db, created.ID)
	require.Equal(t, domain.KnowledgeIndexSucceeded, document.Status)
	require.NotZero(t, document.SegmentCount)
	content := &servermodels.KnowledgeDocumentContent{}
	require.NoError(t, db.NewSelect().Model(content).Where("kdc.document_id = ?", created.ID).Scan(ctx))
	require.Equal(t, probe.markdown, content.Content)

	// 重试读取已有快照，页面内容变化不进入索引。
	probe.markdown = "# 帮助\n\n页面已改版。"
	require.NoError(t, processing.Retry(ctx, identity, base.ID, created.ID))
	runDocumentProcessing(t, db, probe, identity.Workspace.ID, base.ID, created.ID, false)
	require.NoError(t, db.NewSelect().Model(content).Where("kdc.document_id = ?", created.ID).Scan(ctx))
	require.Equal(t, "# 帮助\n\n签收后七天内可以申请退款。", content.Content)

	// 重新抓取更新快照并替换批次。
	published := loadKnowledgeDocument(t, db, created.ID).SegmentBatchID
	require.NoError(t, processing.Refetch(ctx, identity, base.ID, created.ID, ""))
	runDocumentProcessing(t, db, probe, identity.Workspace.ID, base.ID, created.ID, true)
	document = loadKnowledgeDocument(t, db, created.ID)
	require.NoError(t, db.NewSelect().Model(content).Where("kdc.document_id = ?", created.ID).Scan(ctx))
	require.Equal(t, probe.markdown, content.Content)
	require.NotEqual(t, published, document.SegmentBatchID)
	require.Equal(t, domain.KnowledgeIndexSucceeded, document.Status)

	// 重新抓取可以同时更新页面地址。
	require.NoError(t, processing.Refetch(ctx, identity, base.ID, created.ID, "https://example.com/help/refund"))
	document = loadKnowledgeDocument(t, db, created.ID)
	require.Equal(t, "https://example.com/help/refund", document.SourceURL)
	runDocumentProcessing(t, db, probe, identity.Workspace.ID, base.ID, created.ID, true)

	// 抓取失败保留上一批次与上一快照。
	failing := &processingProbe{fetchErr: &webfetch.Error{Code: "url_unreachable"}}
	published = loadKnowledgeDocument(t, db, created.ID).SegmentBatchID
	require.NoError(t, processing.Refetch(ctx, identity, base.ID, created.ID, ""))
	stale := loadKnowledgeDocument(t, db, created.ID)
	runErr := knowledgeaction.NewProcessDocumentAction(db, failing, modelcall.New(db, modelcall.Upstreams{Embedder: failing}, nil), failing, failing).Execute(ctx, knowledgeaction.ProcessInput{
		WorkspaceID: identity.Workspace.ID, KnowledgeBaseID: base.ID, DocumentID: created.ID, SourceKind: stale.SourceKind, FetchPage: true, ProcessingID: stale.ProcessingID,
		ChunkLength: stale.ChunkLength, ChunkOverlap: stale.ChunkOverlap,
		EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension,
	})
	err = knowledgeaction.NewProcessDocumentAction(db, failing, modelcall.New(db, modelcall.Upstreams{Embedder: failing}, nil), failing, failing).FinalizeFailure(ctx, knowledgeaction.ProcessInput{DocumentID: created.ID, ProcessingID: stale.ProcessingID}, runErr)
	require.NoError(t, err)
	document = loadKnowledgeDocument(t, db, created.ID)
	require.Equal(t, domain.KnowledgeIndexFailed, document.Status)
	require.Equal(t, "url_unreachable", document.FailureCode)
	require.Equal(t, published, document.SegmentBatchID)
	require.NoError(t, db.NewSelect().Model(content).Where("kdc.document_id = ?", created.ID).Scan(ctx))
	require.Equal(t, probe.markdown, content.Content)

	// 在线文档不支持重新抓取。
	text, err := knowledgeaction.NewSaveTextDocumentAction(db, tasks).Execute(ctx, identity, base.ID, "", knowledgeaction.TextDocumentInput{Title: "在线说明", Content: "正文"})
	require.NoError(t, err)
	require.ErrorIs(t, processing.Refetch(ctx, identity, base.ID, text.ID, ""), knowledgeaction.ErrDocumentSourceUnsupported)
}
