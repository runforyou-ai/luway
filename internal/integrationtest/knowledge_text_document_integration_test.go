//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// loadKnowledgeDocument 读取当前文档行。
func loadKnowledgeDocument(t *testing.T, db *bun.DB, documentID string) *servermodels.KnowledgeDocument {
	t.Helper()
	document := &servermodels.KnowledgeDocument{}
	if err := db.NewSelect().Model(document).Where("kd.id = ?", documentID).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return document
}

// runDocumentProcessing 按文档当前的任务快照执行一次处理，fetchPage 表示本次是否重新抓取网页。
func runDocumentProcessing(t *testing.T, db *bun.DB, probe *retrievalProbe, organizationID, baseID, documentID string, fetchPage bool) {
	t.Helper()
	document := loadKnowledgeDocument(t, db, documentID)
	err := knowledgeaction.NewProcessDocumentAction(db, probe, probe, probe, probe).Execute(context.Background(), knowledgeaction.ProcessInput{
		OrganizationID: organizationID, KnowledgeBaseID: baseID, DocumentID: documentID, SourceKind: document.SourceKind, FetchPage: fetchPage, ProcessingID: document.ProcessingID,
		ChunkLength: document.ChunkLength, ChunkOverlap: document.ChunkOverlap,
		EmbeddingProviderID: document.EmbeddingProviderID, EmbeddingModelIdentifier: document.EmbeddingModelIdentifier, EmbeddingDimension: document.EmbeddingDimension,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestKnowledgeTextDocumentLifecycle 验证在线文档的创建、索引、改名、正文编辑、标题过滤与召回名称、来源限制与删除清理。
func TestKnowledgeTextDocumentLifecycle(t *testing.T) {
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
	probe := &retrievalProbe{}
	save := knowledgeaction.NewSaveTextDocumentAction(db, newKnowledgeTasks(t, db))

	// 正文为空的保存在校验阶段拒绝，不创建文档。
	if _, err := save.Execute(ctx, identity, base.ID, "", knowledgeaction.TextDocumentInput{Title: "退款说明", Content: "  "}); !errors.As(err, new(*common.FieldError)) {
		t.Fatalf("err=%v", err)
	}

	created, err := save.Execute(ctx, identity, base.ID, "", knowledgeaction.TextDocumentInput{Title: "退款说明", Content: "# 退款\n\n签收后七天内可以申请退款。"})
	if err != nil || created.Name != "退款说明" || created.SourceKind != domain.KnowledgeDocumentSourceText {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	document := loadKnowledgeDocument(t, db, created.ID)
	if document.Status != domain.KnowledgeIndexQueued || document.ProcessingID == "" || document.FileID != "" {
		t.Fatalf("document=%+v", document)
	}

	runDocumentProcessing(t, db, probe, identity.Organization.ID, base.ID, created.ID, false)
	document = loadKnowledgeDocument(t, db, created.ID)
	if document.Status != domain.KnowledgeIndexSucceeded || document.SegmentBatchID != document.ProcessingID || document.SegmentCount == 0 {
		t.Fatalf("document=%+v", document)
	}
	published := document.ProcessingID

	// 只改名称保留已发布批次，不投递新任务。
	renamed, err := knowledgeaction.NewRenameDocumentAction(db).Execute(ctx, identity, base.ID, created.ID, "退款政策说明")
	if err != nil || renamed.Name != "退款政策说明" {
		t.Fatalf("renamed=%+v err=%v", renamed, err)
	}
	document = loadKnowledgeDocument(t, db, created.ID)
	if document.ProcessingID != published || document.Status != domain.KnowledgeIndexSucceeded {
		t.Fatalf("document=%+v", document)
	}

	// 正文未变化时同样不投递新任务。
	if _, err := save.Execute(ctx, identity, base.ID, created.ID, knowledgeaction.TextDocumentInput{Title: "退款政策说明", Content: "# 退款\n\n签收后七天内可以申请退款。"}); err != nil {
		t.Fatal(err)
	}
	if document = loadKnowledgeDocument(t, db, created.ID); document.ProcessingID != published {
		t.Fatalf("document=%+v", document)
	}

	// 正文变化后替换批次，旧分段被清除。
	if _, err := save.Execute(ctx, identity, base.ID, created.ID, knowledgeaction.TextDocumentInput{Title: "退款政策说明", Content: "# 退款\n\n签收后七天内可以申请退款，退款金额原路返回。"}); err != nil {
		t.Fatal(err)
	}
	document = loadKnowledgeDocument(t, db, created.ID)
	if document.ProcessingID == published || document.Status != domain.KnowledgeIndexQueued || document.SegmentBatchID != published {
		t.Fatalf("document=%+v", document)
	}
	runDocumentProcessing(t, db, probe, identity.Organization.ID, base.ID, created.ID, false)
	document = loadKnowledgeDocument(t, db, created.ID)
	if document.SegmentBatchID != document.ProcessingID || document.Status != domain.KnowledgeIndexSucceeded {
		t.Fatalf("document=%+v", document)
	}
	stale, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND segment_batch_id = ?", created.ID, published).Count(ctx)
	if err != nil || stale != 0 {
		t.Fatalf("stale=%d err=%v", stale, err)
	}
	content, err := knowledgeaction.NewDocumentQuery(db).Content(ctx, identity, base.ID, created.ID)
	if err != nil || content.Content != "# 退款\n\n签收后七天内可以申请退款，退款金额原路返回。" {
		t.Fatalf("content=%+v err=%v", content, err)
	}

	// 上传原件来源不支持正文读取、正文编辑与改名。
	uploaded := publishRetrievalDocument(t, db, probe, identity, base, "配送说明.txt", "配送时效按收货地址计算。")
	if _, err := knowledgeaction.NewDocumentQuery(db).Content(ctx, identity, base.ID, uploaded); !errors.Is(err, knowledgeaction.ErrDocumentSourceUnsupported) {
		t.Fatalf("err=%v", err)
	}
	if _, err := save.Execute(ctx, identity, base.ID, uploaded, knowledgeaction.TextDocumentInput{Title: "配送说明", Content: "正文"}); !errors.Is(err, knowledgeaction.ErrDocumentSourceUnsupported) {
		t.Fatalf("err=%v", err)
	}
	if _, err := knowledgeaction.NewRenameDocumentAction(db).Execute(ctx, identity, base.ID, uploaded, "配送政策"); !errors.Is(err, knowledgeaction.ErrDocumentSourceUnsupported) {
		t.Fatalf("err=%v", err)
	}

	// 列表按文档标题过滤，在线文档的类型与大小取自正文。
	titled, err := knowledgeaction.NewDocumentQuery(db).List(ctx, identity, base.ID, knowledgeaction.DocumentListInput{Keyword: "退款政策"})
	if err != nil || len(titled.Documents) != 1 || titled.Documents[0].ID != created.ID {
		t.Fatalf("titled=%+v err=%v", titled, err)
	}
	if record := titled.Documents[0]; record.ContentType != domain.KnowledgeDocumentMarkdownContentType || record.ByteSize != int64(len(content.Content)) {
		t.Fatalf("content_type=%s byte_size=%d", record.ContentType, record.ByteSize)
	}

	// 召回结果的来源名称取文档标题。
	records, err := knowledgeaction.NewRetrievalService(db, probe, probe).Retrieve(ctx, identity, base.ID, "如何申请退款")
	if err != nil || len(records) == 0 || records[0].DocumentID != created.ID || records[0].DocumentName != "退款政策说明" {
		t.Fatalf("records=%+v err=%v", records, err)
	}

	// 删除文档时正文与分段一并清除。
	if err := knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, identity, base.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	contents, err := db.NewSelect().Model((*servermodels.KnowledgeDocumentContent)(nil)).Where("document_id = ?", created.ID).Count(ctx)
	if err != nil || contents != 0 {
		t.Fatalf("contents=%d err=%v", contents, err)
	}
	segments, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ?", created.ID).Count(ctx)
	if err != nil || segments != 0 {
		t.Fatalf("segments=%d err=%v", segments, err)
	}
}
