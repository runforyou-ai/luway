//go:build server

package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"uuid"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/documentconvert"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/embedding"
	"github.com/runforyou-ai/luway/pkg/textsplit"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
)

type processingProbe struct {
	fail       bool
	embedFail  bool
	credential embedding.Credential
	markdown   string
	fetchErr   error
}

// Fetch 返回预设的网页内容，抓取失败时返回预设错误。
func (p *processingProbe) Fetch(context.Context, string) (webfetch.Page, error) {
	if p.fetchErr != nil {
		return webfetch.Page{}, p.fetchErr
	}
	return webfetch.Page{ContentType: webfetch.ContentTypeHTML}, nil
}

// Open 为执行任务提供固定原件。
func (p *processingProbe) Open(context.Context, *servermodels.File) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("原件")), nil
}

// Convert 返回预设正文，或按需模拟转换失败。
func (p *processingProbe) Convert(context.Context, string, io.Reader) (string, error) {
	if p.fail {
		return "", &documentconvert.Error{Code: "parse_failed"}
	}
	if p.markdown != "" {
		return p.markdown, nil
	}
	return "正文", nil
}

// Embed 记录本次凭据并按维度返回定长向量，或按需模拟向量接口失败。
func (p *processingProbe) Embed(_ context.Context, credential embedding.Credential, _ string, dimension int, inputs []string) (embedding.Result, error) {
	p.credential = credential
	if p.embedFail {
		return embedding.Result{}, &embedding.Error{Code: "embedding_failed"}
	}
	vectors := make([][]float32, len(inputs))
	for index := range vectors {
		vectors[index] = make([]float32, dimension)
	}
	return embedding.Result{Vectors: vectors}, nil
}

// TestKnowledgeProcessingRetryAndPublication 验证上传投递、失败重试幂等、参数快照与完整发布。
func TestKnowledgeProcessingRetryAndPublication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	tasks := newKnowledgeTasks(t, db)
	file := uploadedDocumentFile(t, db, installed.Identity, "资料.txt")
	documents, err := knowledgeaction.NewCreateDocumentsAction(db, tasks).Execute(ctx, installed.Identity, base.ID, []string{file.ID})
	if err != nil {
		t.Fatal(err)
	}
	documentID := documents[0].ID
	var document servermodels.KnowledgeDocument
	if err := db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if document.Status != domain.KnowledgeIndexQueued || document.ChunkLength != 512 || document.ProcessingID == "" {
		t.Fatalf("document=%+v", document)
	}
	input := knowledgeaction.ProcessInput{OrganizationID: installed.Identity.Organization.ID, KnowledgeBaseID: base.ID, DocumentID: documentID, ProcessingID: document.ProcessingID, ChunkLength: document.ChunkLength, ChunkOverlap: document.ChunkOverlap, EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension}
	probe := &processingProbe{fail: true}
	worker := knowledgeaction.NewProcessDocumentAction(db, probe, modelcall.New(db, modelcall.Upstreams{Embedder: probe}), probe, probe)
	err = worker.Execute(ctx, input)
	if err == nil {
		t.Fatalf("failure=%v", err)
	}
	if err := worker.FinalizeFailure(ctx, input, err); err != nil {
		t.Fatal(err)
	}
	query := knowledgeaction.NewDocumentQuery(db)
	failed, err := query.Get(ctx, installed.Identity, base.ID, documentID)
	if err != nil || failed.Status != domain.KnowledgeIndexFailed || failed.FailureCode != "parse_failed" {
		t.Fatalf("failure=%+v %v", failed, err)
	}
	// 核验连续重试后生效的任务标识。
	retry := knowledgeaction.NewDocumentProcessing(db, tasks)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			if err := retry.Retry(ctx, installed.Identity, base.ID, documentID); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if err := db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if document.ProcessingID == input.ProcessingID {
		t.Fatal("retry reused failed execution")
	}
	count, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).Where("action_name = ? AND payload->>'documentId' = ?", knowledgeaction.ProcessDocumentActionName, documentID).Count(ctx)
	if err != nil || count != 3 {
		t.Fatalf("tasks=%d %v", count, err)
	}
	// 核验旧任务成功和失败后的当前任务状态。
	if err := worker.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := worker.FinalizeFailure(ctx, input, errors.New("old failure")); err != nil {
		t.Fatal(err)
	}
	input.ProcessingID = document.ProcessingID
	probe.fail = false
	if err := worker.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := worker.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	completed, err := query.Get(ctx, installed.Identity, base.ID, documentID)
	if err != nil || completed.Status != domain.KnowledgeIndexSucceeded || completed.SegmentCount != 1 || completed.SegmentBatchID != input.ProcessingID {
		t.Fatalf("completed=%+v %v", completed, err)
	}
	// 核验任务载荷的向量配置快照、执行时解析的模型凭据和落库的分段维度。
	snapshots, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).
		Where("action_name = ? AND payload->>'documentId' = ? AND payload->>'embeddingModelId' = ? AND payload->>'embeddingDimension' = '1024'", knowledgeaction.ProcessDocumentActionName, documentID, base.EmbeddingModelID).
		Count(ctx)
	if err != nil || snapshots == 0 {
		t.Fatalf("snapshots=%d %v", snapshots, err)
	}
	if probe.credential.BaseURL != "https://models.test/v1" || probe.credential.APIKey != "test-key" {
		t.Fatalf("credential=%+v", probe.credential)
	}
	stored, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND embedding_dimension = 1024 AND embedding IS NOT NULL", documentID).Count(ctx)
	if err != nil || stored != 1 {
		t.Fatalf("stored=%d %v", stored, err)
	}
	if err := retry.Retry(ctx, installed.Identity, base.ID, documentID); err != nil {
		t.Fatal(err)
	}
	if err := knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, installed.Identity, base.ID, documentID); err != nil {
		t.Fatal(err)
	}
	count, err = db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ?", documentID).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("remaining=%d %v", count, err)
	}
	if err := worker.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
}

// TestKnowledgeRetryAllStates 验证所有状态可按新配置重试，旧任务失效且已发布内容持续可读。
func TestKnowledgeRetryAllStates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	owner, base := newDocumentFixture(t, db)
	tasks := newKnowledgeTasks(t, db)
	docs, err := knowledgeaction.NewCreateDocumentsAction(db, tasks).Execute(ctx, owner.Identity, base.ID, []string{uploadedDocumentFile(t, db, owner.Identity, "重新分段.txt").ID})
	if err != nil {
		t.Fatal(err)
	}
	var document servermodels.KnowledgeDocument
	if err := db.NewSelect().Model(&document).Where("kd.id = ?", docs[0].ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	input := knowledgeaction.ProcessInput{OrganizationID: owner.Identity.Organization.ID, KnowledgeBaseID: base.ID, DocumentID: document.ID, ProcessingID: document.ProcessingID, ChunkLength: 512, ChunkOverlap: 50, EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension}
	probe := &processingProbe{}
	worker := knowledgeaction.NewProcessDocumentAction(db, probe, modelcall.New(db, modelcall.Upstreams{Embedder: probe}), probe, probe)
	if err := worker.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	published := input.ProcessingID
	if _, err := db.NewUpdate().Model((*servermodels.KnowledgeBase)(nil)).Set("chunk_length = 768").Set("chunk_overlap = 80").Where("id = ?", base.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	retry := knowledgeaction.NewDocumentProcessing(db, tasks)
	states := []domain.KnowledgeIndexStatus{domain.KnowledgeIndexInitial, domain.KnowledgeIndexQueued, domain.KnowledgeIndexFetching, domain.KnowledgeIndexConverting, domain.KnowledgeIndexSplitting, domain.KnowledgeIndexEmbedding, domain.KnowledgeIndexPublishing, domain.KnowledgeIndexSucceeded, domain.KnowledgeIndexFailed, domain.KnowledgeIndexCancelled}
	for _, state := range states {
		if _, err := db.NewUpdate().Model(&document).Set("status = ?", state).WherePK().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := retry.Retry(ctx, owner.Identity, base.ID, document.ID); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if err := db.NewSelect().Model(&document).Where("kd.id = ?", document.ID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if document.ProcessingID == input.ProcessingID || document.Status != domain.KnowledgeIndexQueued || document.ChunkLength != 768 || document.ChunkOverlap != 80 || document.SegmentBatchID != published {
			t.Fatalf("%s: %+v", state, document)
		}
		if err := worker.Execute(ctx, input); err != nil {
			t.Fatal(err)
		}
		if err := worker.FinalizeFailure(ctx, input, errors.New("old failure")); err != nil {
			t.Fatal(err)
		}
		input.ProcessingID, input.ChunkLength, input.ChunkOverlap = document.ProcessingID, document.ChunkLength, document.ChunkOverlap
	}
	// 当前重试失败仍保留已完成分段，再次成功才替换正文。
	probe.fail = true
	if err := worker.FinalizeFailure(ctx, input, worker.Execute(ctx, input)); err != nil {
		t.Fatal(err)
	}
	count, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND segment_batch_id = ?", document.ID, published).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("published=%d %v", count, err)
	}
	if err := retry.Retry(ctx, owner.Identity, base.ID, document.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.NewSelect().Model(&document).Where("kd.id = ?", document.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	input.ProcessingID = document.ProcessingID
	probe.fail = false
	if err := worker.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	count, err = db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND segment_batch_id = ?", document.ID, published).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("old batch=%d %v", count, err)
	}
	if err := db.NewSelect().Model(&document).Where("kd.id = ?", document.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if document.Status != domain.KnowledgeIndexSucceeded || document.SegmentBatchID != input.ProcessingID {
		t.Fatalf("completed=%+v", document)
	}
}

// TestKnowledgeProcessingMissingFile 验证原件记录缺失会结束为可重试的失败状态。
func TestKnowledgeProcessingMissingFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	owner, base := newDocumentFixture(t, db)
	file := uploadedDocumentFile(t, db, owner.Identity, "缺失原件.txt")
	docs, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, owner.Identity, base.ID, []string{file.ID})
	if err != nil {
		t.Fatal(err)
	}
	var document servermodels.KnowledgeDocument
	if err := db.NewSelect().Model(&document).Where("kd.id = ?", docs[0].ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewDelete().Model(file).WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	probe := &processingProbe{}
	worker := knowledgeaction.NewProcessDocumentAction(db, probe, modelcall.New(db, modelcall.Upstreams{Embedder: probe}), probe, probe)
	input := knowledgeaction.ProcessInput{OrganizationID: owner.Identity.Organization.ID, KnowledgeBaseID: base.ID, DocumentID: document.ID, ProcessingID: document.ProcessingID, ChunkLength: 512, ChunkOverlap: 50, EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension}
	err = worker.Execute(ctx, input)
	if err == nil {
		t.Fatalf("failure=%v", err)
	}
	if err := worker.FinalizeFailure(ctx, input, err); err != nil {
		t.Fatal(err)
	}
	if err := db.NewSelect().Model(&document).Where("kd.id = ?", document.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if document.Status != domain.KnowledgeIndexFailed || document.FailureCode != "file_read_failed" {
		t.Fatalf("document=%+v", document)
	}
}

// insertSegments 按固定批次写入连续编号的分段记录。
func insertSegments(t *testing.T, db *bun.DB, organizationID, baseID, documentID, batchID string, count int) []string {
	t.Helper()
	ids := make([]string, 0, count)
	for position := 1; position <= count; position++ {
		id := uuid.NewV7().String()
		_, err := db.ExecContext(context.Background(), "INSERT INTO public.knowledge_segments(id, organization_id, knowledge_base_id, source_type, source_id, segment_batch_id, position, character_count, context, content) VALUES (?, ?, ?, ?, ?, ?, ?, 4, '', ?)",
			id, organizationID, baseID, "document", documentID, batchID, position, fmt.Sprintf("第%d段正文", position))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestKnowledgeProcessingPublishesSegments 验证多段发布后的正文、序号、确定性编号和空正文失败。
func TestKnowledgeProcessingPublishesSegments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	owner, base := newDocumentFixture(t, db)
	docs, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, owner.Identity, base.ID, []string{uploadedDocumentFile(t, db, owner.Identity, "多段.txt").ID})
	if err != nil {
		t.Fatal(err)
	}
	document := &servermodels.KnowledgeDocument{}
	if err := db.NewSelect().Model(document).Where("kd.id = ?", docs[0].ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	markdown := "# 采购合同\n" + strings.Repeat("合同正文。", 400)
	probe := &processingProbe{markdown: markdown}
	input := knowledgeaction.ProcessInput{
		OrganizationID: owner.Identity.Organization.ID, KnowledgeBaseID: base.ID, DocumentID: document.ID,
		ProcessingID: document.ProcessingID, ChunkLength: 256, ChunkOverlap: 50,
		EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension,
	}
	action := knowledgeaction.NewProcessDocumentAction(db, probe, modelcall.New(db, modelcall.Upstreams{Embedder: probe}), probe, probe)
	if err := action.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	expected := textsplit.Split(markdown, input.ChunkLength, input.ChunkOverlap)
	if len(expected) < 2 || expected[1].Context != "# 采购合同" {
		t.Fatalf("样本应切出带标题路径的多段，实际 %+v", expected)
	}
	if err := db.NewSelect().Model(document).Where("kd.id = ?", document.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if document.Status != domain.KnowledgeIndexSucceeded || document.SegmentBatchID != input.ProcessingID || document.SegmentCount != len(expected) {
		t.Fatalf("document=%+v 期望 %d 段", document, len(expected))
	}
	// 读回发布批次，核对上下文、正文、序号与按任务标识确定的分段编号。
	page, err := knowledgeaction.NewDocumentQuery(db).Segments(ctx, owner.Identity, base.ID, document.ID, knowledgeaction.SegmentQueryInput{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Segments) != len(expected) || page.Total != len(expected) {
		t.Fatalf("page=%d 段，总数 %d", len(page.Segments), page.Total)
	}
	namespace := uuid.MustParse(input.ProcessingID)
	for index, segment := range page.Segments {
		want := expected[index]
		if segment.Position != want.Position || segment.Context != want.Context || segment.Content != want.Content || segment.CharacterCount != want.CharacterCount {
			t.Fatalf("分段 %d = %+v，期望 %+v", index+1, segment, want)
		}
		if segment.ID != common.NewUUIDv5(namespace, strconv.Itoa(want.Position)).String() {
			t.Fatalf("分段 %d 编号 = %s", index+1, segment.ID)
		}
	}
	// 转换结果为空白时按空正文失败，并保留上次成功的批次。
	empty := input
	empty.ProcessingID = uuid.NewV7().String()
	if _, err := db.NewUpdate().Model(document).Set("processing_id = ?", empty.ProcessingID).Set("status = ?", domain.KnowledgeIndexQueued).WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	probe.markdown = " \n\t"
	runErr := action.Execute(ctx, empty)
	var failure *knowledgeaction.ProcessError
	if !errors.As(runErr, &failure) || failure.Code != "empty_content" || failure.Stage != domain.KnowledgeIndexSplitting {
		t.Fatalf("empty=%v", runErr)
	}
	if err := action.FinalizeFailure(ctx, empty, runErr); err != nil {
		t.Fatal(err)
	}
	if err := db.NewSelect().Model(document).Where("kd.id = ?", document.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if document.Status != domain.KnowledgeIndexFailed || document.FailureCode != "empty_content" || document.SegmentBatchID != input.ProcessingID {
		t.Fatalf("失败后 document=%+v", document)
	}
}

// TestKnowledgeSegmentsScopeAndBatch 验证企业与批次校验、分页边界和锚点定位。
func TestKnowledgeSegmentsScopeAndBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	owner, base := newDocumentFixture(t, db)
	docs, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, owner.Identity, base.ID, []string{uploadedDocumentFile(t, db, owner.Identity, "分段.txt").ID})
	if err != nil {
		t.Fatal(err)
	}
	document := docs[0]
	query := knowledgeaction.NewDocumentQuery(db)
	// 未发布批次的文档不提供分段。
	if _, err := query.Segments(ctx, owner.Identity, base.ID, document.ID, knowledgeaction.SegmentQueryInput{}); !errors.Is(err, knowledgeaction.ErrSegmentsNotReady) {
		t.Fatalf("not ready=%v", err)
	}
	batch := uuid.NewV7().String()
	if _, err := db.NewUpdate().Model((*servermodels.KnowledgeDocument)(nil)).Set("segment_batch_id = ?", batch).Set("segment_count = 25").Where("id = ?", document.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ids := insertSegments(t, db, owner.Identity.Organization.ID, base.ID, document.ID, batch, 25)
	// 旧批次的残留分段不得混入当前批次的读取结果。
	insertSegments(t, db, owner.Identity.Organization.ID, base.ID, document.ID, uuid.NewV7().String(), 25)

	if _, err := query.Segments(ctx, owner.Identity, base.ID, document.ID, knowledgeaction.SegmentQueryInput{SegmentBatchID: uuid.NewV7().String()}); !errors.Is(err, knowledgeaction.ErrSegmentStale) {
		t.Fatalf("stale=%v", err)
	}
	other, _ := newDocumentFixture(t, db)
	if _, err := query.Segments(ctx, other.Identity, base.ID, document.ID, knowledgeaction.SegmentQueryInput{}); err == nil {
		t.Fatal("foreign query allowed")
	}
	for _, input := range []knowledgeaction.SegmentQueryInput{
		{SegmentBatchID: batch, AnchorSegmentID: ids[0], Page: 2},
		{AnchorSegmentID: ids[0]},
		{SegmentBatchID: batch, AnchorSegmentID: "not-a-uuid"},
		{PageSize: 101},
		{Page: -1},
	} {
		if _, err := query.Segments(ctx, owner.Identity, base.ID, document.ID, input); !errors.Is(err, knowledgeaction.ErrSegmentQueryInvalid) {
			t.Fatalf("invalid input %+v = %v", input, err)
		}
	}
	if _, err := query.Segments(ctx, owner.Identity, base.ID, document.ID, knowledgeaction.SegmentQueryInput{SegmentBatchID: batch, AnchorSegmentID: uuid.NewV7().String()}); !errors.Is(err, knowledgeaction.ErrSegmentStale) {
		t.Fatalf("missing anchor=%v", err)
	}

	// 默认读取首页，并核对正文与来源信息。
	page, err := query.Segments(ctx, owner.Identity, base.ID, document.ID, knowledgeaction.SegmentQueryInput{})
	if err != nil {
		t.Fatal(err)
	}
	if page.SegmentBatchID != batch || page.Page != 1 || page.PageSize != 20 || page.Total != 25 || len(page.Segments) != 20 {
		t.Fatalf("page=%+v", page)
	}
	first := page.Segments[0]
	if first.ID != ids[0] || first.Position != 1 || first.Content != "第1段正文" || first.CharacterCount != 4 {
		t.Fatalf("segment=%+v", first)
	}
	if page.Segments[19].Position != 20 {
		t.Fatalf("last=%+v", page.Segments[19])
	}

	// 末页只返回剩余分段。
	page, err = query.Segments(ctx, owner.Identity, base.ID, document.ID, knowledgeaction.SegmentQueryInput{Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Page != 2 || len(page.Segments) != 5 || page.Segments[0].Position != 21 {
		t.Fatalf("page=%+v", page)
	}

	// 锚点按其之前的分段数量落到第二页。
	page, err = query.Segments(ctx, owner.Identity, base.ID, document.ID, knowledgeaction.SegmentQueryInput{SegmentBatchID: batch, AnchorSegmentID: ids[20]})
	if err != nil {
		t.Fatal(err)
	}
	if page.Page != 2 || page.AnchorSegmentID != ids[20] || page.AnchorPosition != 21 || page.Segments[0].ID != ids[20] {
		t.Fatalf("anchor page=%+v", page)
	}
}
