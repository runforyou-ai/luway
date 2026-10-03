//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/webfetch"
)

// TestKnowledgeWebDocumentLifecycle 验证网页导入的抓取、快照、重试不出网、重新抓取与失败保留。
func TestKnowledgeWebDocumentLifecycle(t *testing.T) {
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
	probe := &retrievalProbe{}
	create := knowledgeaction.NewCreateWebDocumentAction(db, tasks)
	processing := knowledgeaction.NewDocumentProcessing(db, tasks)

	// 非 http 与 https 的地址在保存期拒绝。
	if _, err := create.Execute(ctx, identity, base.ID, knowledgeaction.WebDocumentInput{Title: "帮助中心", SourceURL: "ftp://example.com/help"}); !errors.As(err, new(*common.FieldError)) {
		t.Fatalf("err=%v", err)
	}

	created, err := create.Execute(ctx, identity, base.ID, knowledgeaction.WebDocumentInput{Title: "帮助中心", SourceURL: "https://example.com/help#section"})
	if err != nil || created.SourceKind != domain.KnowledgeDocumentSourceWeb || created.SourceURL != "https://example.com/help" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	// 同一知识库中重复导入同一页面被拒绝。
	if _, err := create.Execute(ctx, identity, base.ID, knowledgeaction.WebDocumentInput{Title: "帮助中心副本", SourceURL: "https://example.com/help"}); !errors.Is(err, knowledgeaction.ErrDocumentURLDuplicate) {
		t.Fatalf("err=%v", err)
	}

	probe.markdown = "# 帮助\n\n签收后七天内可以申请退款。"
	runDocumentProcessing(t, db, probe, identity.Organization.ID, base.ID, created.ID, true)
	document := loadKnowledgeDocument(t, db, created.ID)
	if document.Status != domain.KnowledgeIndexSucceeded || document.SegmentCount == 0 {
		t.Fatalf("document=%+v", document)
	}
	content := &servermodels.KnowledgeDocumentContent{}
	if err := db.NewSelect().Model(content).Where("kdc.document_id = ?", created.ID).Scan(ctx); err != nil || content.Content != probe.markdown {
		t.Fatalf("content=%+v err=%v", content, err)
	}

	// 重试读取已有快照，页面内容变化不进入索引。
	probe.markdown = "# 帮助\n\n页面已改版。"
	if err := processing.Retry(ctx, identity, base.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	runDocumentProcessing(t, db, probe, identity.Organization.ID, base.ID, created.ID, false)
	if err := db.NewSelect().Model(content).Where("kdc.document_id = ?", created.ID).Scan(ctx); err != nil || content.Content != "# 帮助\n\n签收后七天内可以申请退款。" {
		t.Fatalf("content=%+v err=%v", content, err)
	}

	// 重新抓取更新快照并替换批次。
	published := loadKnowledgeDocument(t, db, created.ID).SegmentBatchID
	if err := processing.Refetch(ctx, identity, base.ID, created.ID, ""); err != nil {
		t.Fatal(err)
	}
	runDocumentProcessing(t, db, probe, identity.Organization.ID, base.ID, created.ID, true)
	document = loadKnowledgeDocument(t, db, created.ID)
	if err := db.NewSelect().Model(content).Where("kdc.document_id = ?", created.ID).Scan(ctx); err != nil || content.Content != probe.markdown {
		t.Fatalf("content=%+v err=%v", content, err)
	}
	if document.SegmentBatchID == published || document.Status != domain.KnowledgeIndexSucceeded {
		t.Fatalf("document=%+v", document)
	}

	// 重新抓取可以同时更新页面地址。
	if err := processing.Refetch(ctx, identity, base.ID, created.ID, "https://example.com/help/refund"); err != nil {
		t.Fatal(err)
	}
	if document = loadKnowledgeDocument(t, db, created.ID); document.SourceURL != "https://example.com/help/refund" {
		t.Fatalf("document=%+v", document)
	}
	runDocumentProcessing(t, db, probe, identity.Organization.ID, base.ID, created.ID, true)

	// 抓取失败保留上一批次与上一快照。
	failing := &processingProbe{fetchErr: &webfetch.Error{Code: "url_unreachable"}}
	published = loadKnowledgeDocument(t, db, created.ID).SegmentBatchID
	if err := processing.Refetch(ctx, identity, base.ID, created.ID, ""); err != nil {
		t.Fatal(err)
	}
	stale := loadKnowledgeDocument(t, db, created.ID)
	runErr := knowledgeaction.NewProcessDocumentAction(db, failing, modelcall.New(db, modelcall.Upstreams{Embedder: failing}), failing, failing).Execute(ctx, knowledgeaction.ProcessInput{
		OrganizationID: identity.Organization.ID, KnowledgeBaseID: base.ID, DocumentID: created.ID, SourceKind: stale.SourceKind, FetchPage: true, ProcessingID: stale.ProcessingID,
		ChunkLength: stale.ChunkLength, ChunkOverlap: stale.ChunkOverlap,
		EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension,
	})
	if err := knowledgeaction.NewProcessDocumentAction(db, failing, modelcall.New(db, modelcall.Upstreams{Embedder: failing}), failing, failing).FinalizeFailure(ctx, knowledgeaction.ProcessInput{DocumentID: created.ID, ProcessingID: stale.ProcessingID}, runErr); err != nil {
		t.Fatal(err)
	}
	document = loadKnowledgeDocument(t, db, created.ID)
	if document.Status != domain.KnowledgeIndexFailed || document.FailureCode != "url_unreachable" || document.SegmentBatchID != published {
		t.Fatalf("document=%+v", document)
	}
	if err := db.NewSelect().Model(content).Where("kdc.document_id = ?", created.ID).Scan(ctx); err != nil || content.Content != probe.markdown {
		t.Fatalf("content=%+v err=%v", content, err)
	}

	// 在线文档不支持重新抓取。
	text, err := knowledgeaction.NewSaveTextDocumentAction(db, tasks).Execute(ctx, identity, base.ID, "", knowledgeaction.TextDocumentInput{Title: "在线说明", Content: "正文"})
	if err != nil {
		t.Fatal(err)
	}
	if err := processing.Refetch(ctx, identity, base.ID, text.ID, ""); !errors.Is(err, knowledgeaction.ErrDocumentSourceUnsupported) {
		t.Fatalf("err=%v", err)
	}
}
