//go:build server

package integrationtest

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/knowledgeretrieval"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/embedding"
	"github.com/runforyou-ai/luway/pkg/rerank"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
)

// retrievalTopics 按关键词把文本映射到向量分量，让向量路只在同主题文本之间距离为零。
var retrievalTopics = []string{"退款", "发票", "配送"}

type retrievalProbe struct {
	markdown  string
	embedFail bool
	reranked  int
	embedMu   sync.Mutex
	embedded  [][]string
}

// Fetch 返回固定网页内容。
func (p *retrievalProbe) Fetch(context.Context, string) (webfetch.Page, error) {
	return webfetch.Page{ContentType: webfetch.ContentTypeHTML, Body: []byte(p.markdown)}, nil
}

// Open 提供固定原件。
func (p *retrievalProbe) Open(context.Context, *servermodels.File) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("原件")), nil
}

// Convert 返回当前预设正文。
func (p *retrievalProbe) Convert(context.Context, string, io.Reader) (string, error) {
	return p.markdown, nil
}

// Embed 按文本包含的主题关键词生成单位向量，未命中主题的文本落在独立分量。
func (p *retrievalProbe) Embed(_ context.Context, _ embedding.Credential, _ string, dimension int, inputs []string) ([][]float32, error) {
	p.embedMu.Lock()
	p.embedded = append(p.embedded, slices.Clone(inputs))
	p.embedMu.Unlock()
	if p.embedFail {
		return nil, &embedding.Error{Code: "embedding_failed"}
	}
	vectors := make([][]float32, len(inputs))
	for index, input := range inputs {
		vectors[index] = make([]float32, dimension)
		component := len(retrievalTopics)
		for topic, keyword := range retrievalTopics {
			if strings.Contains(input, keyword) {
				component = topic
			}
		}
		vectors[index][component] = 1
	}
	return vectors, nil
}

// Rerank 记录调用次数，按候选是否包含退款、发票关键词给出固定相关性。
func (p *retrievalProbe) Rerank(_ context.Context, _ rerank.Credential, _, _ string, documents []string, _ int) ([]rerank.Score, error) {
	p.reranked++
	scores := make([]rerank.Score, 0, len(documents))
	for index, document := range documents {
		relevance := 0.1
		if strings.Contains(document, "退款") {
			relevance = 1
		} else if strings.Contains(document, "发票") {
			relevance = 0.8
		}
		scores = append(scores, rerank.Score{Index: index, Relevance: relevance})
	}
	return scores, nil
}

// publishRetrievalDocument 上传并处理一篇文档，返回文档编号。
func publishRetrievalDocument(t *testing.T, db *bun.DB, probe *retrievalProbe, identity *servermodels.Identity, base *knowledgeaction.Record, name, markdown string) string {
	t.Helper()
	ctx := context.Background()
	file := uploadedDocumentFile(t, db, identity, name)
	documents, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, []string{file.ID})
	if err != nil {
		t.Fatal(err)
	}
	var document servermodels.KnowledgeDocument
	if err := db.NewSelect().Model(&document).Where("kd.id = ?", documents[0].ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	probe.markdown = markdown
	worker := knowledgeaction.NewProcessDocumentAction(db, probe, probe, probe, probe)
	err = worker.Execute(ctx, knowledgeaction.ProcessInput{
		OrganizationID: identity.Organization.ID, KnowledgeBaseID: base.ID, DocumentID: document.ID, ProcessingID: document.ProcessingID,
		ChunkLength: document.ChunkLength, ChunkOverlap: document.ChunkOverlap,
		EmbeddingProviderID: document.EmbeddingProviderID, EmbeddingModelIdentifier: document.EmbeddingModelIdentifier, EmbeddingDimension: document.EmbeddingDimension,
	})
	if err != nil {
		t.Fatal(err)
	}
	return document.ID
}

// TestKnowledgeHybridRetrieval 验证词法与向量两路召回、名次融合、重排得分、相关性阈值过滤、单路失败保留、企业隔离与游标阅读。
func TestKnowledgeHybridRetrieval(t *testing.T) {
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
	service := knowledgeaction.NewRetrievalService(db, probe, probe)

	if _, err := service.Retrieve(ctx, identity, base.ID, "退款"); !errors.Is(err, knowledgeaction.ErrRetrievalNotReady) {
		t.Fatalf("err=%v", err)
	}
	refundID := publishRetrievalDocument(t, db, probe, identity, base, "退款政策.txt", strings.Repeat("签收后七天内可以申请退款，退款金额原路返回。", 30))
	invoiceID := publishRetrievalDocument(t, db, probe, identity, base, "发票说明.txt", "下单时可以选择开具电子发票。")
	deliveryID := publishRetrievalDocument(t, db, probe, identity, base, "配送说明.txt", "配送时效按收货地址计算。")

	// 词法路与向量路都命中退款文档，最终分数为重排得分。
	records, err := service.Retrieve(ctx, identity, base.ID, "如何申请退款")
	if err != nil || len(records) == 0 || records[0].DocumentID != refundID || records[0].DocumentName != "退款政策.txt" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	if records[0].LexicalRank == 0 || records[0].VectorRank == 0 || records[0].Score != 1 || records[0].SegmentBatchID == "" || records[0].Position == 0 {
		t.Fatalf("first=%+v", records[0])
	}
	if len(records) > base.RetrievalCount || probe.reranked != 1 {
		t.Fatalf("count=%d limit=%d reranked=%d", len(records), base.RetrievalCount, probe.reranked)
	}
	// 两路都把发票文档排在首位，重排后退款文档仍按得分排在前面。
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	if err != nil || len(records) < 2 || records[0].DocumentID != refundID || records[0].LexicalRank != 0 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	for _, record := range records {
		if record.Score < base.RetrievalScoreThreshold {
			t.Fatalf("below threshold record=%+v", record)
		}
	}

	// 多知识库来源经统一融合返回，并可按游标读取相邻分段。
	sources, err := service.Sources(ctx, identity.Organization.ID, []string{base.ID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{"退款", "退款金额"}})
	if err != nil || len(result.Records) == 0 || result.Records[0].KnowledgeBaseID != base.ID || result.Records[0].DocumentID != refundID {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var cursor knowledgeretrieval.Cursor
	for _, record := range result.Records {
		if record.DocumentID == refundID && record.Position == 1 {
			cursor = record.Cursor
		}
	}
	window, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor, After: 1})
	if err != nil || len(window.Records) != 2 || window.Records[0].SegmentID != cursor.SegmentID || window.Records[1].Position != 2 {
		t.Fatalf("cursor=%+v window=%+v err=%v", cursor, window, err)
	}

	// 文档重新发布后，旧批次游标失效，新检索结果的游标可读取相邻分段。
	if err := knowledgeaction.NewDocumentProcessing(db, newKnowledgeTasks(t, db)).Retry(ctx, identity, base.ID, refundID); err != nil {
		t.Fatal(err)
	}
	var republished servermodels.KnowledgeDocument
	if err := db.NewSelect().Model(&republished).Where("kd.id = ?", refundID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	probe.markdown = strings.Repeat("签收后七天内可以申请退款，退款金额原路返回。", 30)
	if err := knowledgeaction.NewProcessDocumentAction(db, probe, probe, probe, probe).Execute(ctx, knowledgeaction.ProcessInput{
		OrganizationID: identity.Organization.ID, KnowledgeBaseID: base.ID, DocumentID: refundID, ProcessingID: republished.ProcessingID,
		ChunkLength: republished.ChunkLength, ChunkOverlap: republished.ChunkOverlap,
		EmbeddingProviderID: republished.EmbeddingProviderID, EmbeddingModelIdentifier: republished.EmbeddingModelIdentifier, EmbeddingDimension: republished.EmbeddingDimension,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor, After: 1}); !errors.Is(err, knowledgeaction.ErrSegmentStale) {
		t.Fatalf("err=%v", err)
	}
	stale := cursor
	result, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{"退款", "退款金额"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range result.Records {
		if record.DocumentID == refundID && record.Position == 1 {
			cursor = record.Cursor
		}
	}
	if cursor.SegmentBatchID == stale.SegmentBatchID {
		t.Fatalf("cursor=%+v result=%+v", cursor, result)
	}
	window, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor, After: 1})
	if err != nil || len(window.Records) != 2 || window.Records[0].SegmentID != cursor.SegmentID || window.Records[1].Cursor.SegmentBatchID != cursor.SegmentBatchID {
		t.Fatalf("cursor=%+v window=%+v err=%v", cursor, window, err)
	}

	// 向量路失败时保留词法路结果。
	probe.embedFail = true
	records, err = service.Retrieve(ctx, identity, base.ID, "退款")
	if err != nil || len(records) == 0 || records[0].DocumentID != refundID || records[0].VectorRank != 0 || records[0].LexicalRank != 1 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	// 没有词法词元时向量路失败即整体失败。
	if _, err := service.Retrieve(ctx, identity, base.ID, "什么"); err == nil {
		t.Fatal("expected embedding failure")
	}
	probe.embedFail = false

	// 召回数量限制作用于重排后的结果。
	input := newKnowledgeBaseInput(t, db, identity, base.Name, base.Category)
	input.EmbeddingProviderID, input.RerankProviderID, input.RetrievalCount = base.EmbeddingProviderID, base.EmbeddingProviderID, 2
	if _, err := knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, input); err != nil {
		t.Fatal(err)
	}
	records, err = service.Retrieve(ctx, identity, base.ID, "如何申请退款")
	if err != nil || len(records) != 2 || records[0].DocumentID != refundID || records[1].DocumentID != refundID {
		t.Fatalf("records=%+v err=%v", records, err)
	}

	// 相关性阈值为 0 时保留低分候选。
	input.RetrievalCount, input.RetrievalScoreThreshold = 20, 0
	if _, err := knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, input); err != nil {
		t.Fatal(err)
	}
	records, err = service.Retrieve(ctx, identity, base.ID, "配送")
	if err != nil || !slices.ContainsFunc(records, func(record knowledgeaction.RetrievalRecord) bool { return record.DocumentID == deliveryID }) {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	// 重排得分等于相关性阈值的候选仍返回。
	input.RetrievalScoreThreshold = 0.8
	if _, err := knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, input); err != nil {
		t.Fatal(err)
	}
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	if err != nil || !slices.ContainsFunc(records, func(record knowledgeaction.RetrievalRecord) bool { return record.DocumentID == invoiceID }) ||
		slices.ContainsFunc(records, func(record knowledgeaction.RetrievalRecord) bool { return record.DocumentID == deliveryID }) {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	// 提高阈值后只返回重排得分达到阈值的候选。
	input.RetrievalScoreThreshold = 0.9
	if _, err := knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, input); err != nil {
		t.Fatal(err)
	}
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	if err != nil || len(records) != 2 || records[0].DocumentID != refundID || records[1].DocumentID != refundID {
		t.Fatalf("records=%+v err=%v", records, err)
	}

	if _, err := service.Retrieve(ctx, identity, base.ID, "   "); !errors.Is(err, knowledgeaction.ErrRetrievalQueryInvalid) {
		t.Fatalf("err=%v", err)
	}
	other, _ := newDocumentFixture(t, db)
	if _, err := service.Retrieve(ctx, other.Identity, base.ID, "退款"); !errors.Is(err, knowledgeaction.ErrNotFound) {
		t.Fatalf("err=%v", err)
	}

	// 文档删除后旧游标不可读取。
	if err := knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, identity, base.ID, refundID); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor}); !errors.Is(err, knowledgeaction.ErrSegmentStale) {
		t.Fatalf("err=%v", err)
	}
	// 剩余候选全部低于相关性阈值时返回空结果。
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	if err != nil || len(records) != 0 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

// publishQAEntry 保存一条问答并执行索引任务，返回条目编号。
func publishQAEntry(t *testing.T, db *bun.DB, probe *retrievalProbe, identity *servermodels.Identity, base *knowledgeaction.Record, input knowledgeaction.QAInput) string {
	t.Helper()
	ctx := context.Background()
	entry, err := knowledgeaction.NewSaveQAEntryAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, "", input)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := qaProcessInput(t, db, identity.Organization.ID, base.ID, entry.ID)
	if err := knowledgeaction.NewProcessQAEntryAction(db, probe).Execute(ctx, task); err != nil {
		t.Fatal(err)
	}
	return entry.ID
}

// TestKnowledgeQARetrieval 验证问答库的就绪判断、问题与答案片段命中折叠为条目、低于相关性阈值的条目不返回、完整答案交付以及游标读取与失效。
func TestKnowledgeQARetrieval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	identity, base := newQAFixture(t, db)
	probe := &retrievalProbe{}
	service := knowledgeaction.NewRetrievalService(db, probe, probe)
	if _, err := service.Retrieve(ctx, identity, base.ID, "退款"); !errors.Is(err, knowledgeaction.ErrRetrievalNotReady) {
		t.Fatalf("err=%v", err)
	}
	answer := strings.Repeat("进入订单详情申请退款，审核通过后退款原路返回。", 40)
	refundID := publishQAEntry(t, db, probe, identity, base, knowledgeaction.QAInput{Question: "如何退款？", Answer: answer, SimilarQuestions: []knowledgeaction.QASimilarQuestion{{Content: "退款入口在哪里"}, {Content: "怎么申请退款"}}})
	invoiceID := publishQAEntry(t, db, probe, identity, base, knowledgeaction.QAInput{Question: "怎么开发票", Answer: "下单时选择电子发票。"})
	deliveryID := publishQAEntry(t, db, probe, identity, base, knowledgeaction.QAInput{Question: "配送要多久", Answer: "配送时效按收货地址计算。"})

	// 主问题、相似问题和多段答案都命中退款条目，折叠后只返回一条并携带完整答案。
	records, err := service.Retrieve(ctx, identity, base.ID, "怎么申请退款")
	if err != nil || len(records) == 0 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	first := records[0]
	if first.DocumentID != refundID || first.SegmentID != refundID || first.Position != 1 || first.DocumentName != "如何退款？" || first.Answer != answer || first.Score != 1 {
		t.Fatalf("first=%+v", first)
	}
	if first.LexicalRank == 0 || first.VectorRank == 0 || !strings.Contains(first.Content, "退款") {
		t.Fatalf("first=%+v", first)
	}
	for _, record := range records[1:] {
		if record.DocumentID == refundID {
			t.Fatalf("refund entry not folded: %+v", records)
		}
		if record.Answer == "" || record.SegmentID != record.DocumentID {
			t.Fatalf("record=%+v", record)
		}
	}
	if slices.ContainsFunc(records, func(record knowledgeaction.RetrievalRecord) bool { return record.DocumentID == deliveryID }) {
		t.Fatalf("below threshold entry returned: %+v", records)
	}
	if len(records) > base.RetrievalCount {
		t.Fatalf("count=%d limit=%d", len(records), base.RetrievalCount)
	}
	// 发票查询命中发票条目并返回其答案。
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record.DocumentID == invoiceID {
			found = record.Answer == "下单时选择电子发票。" && record.DocumentName == "怎么开发票"
		}
	}
	if !found {
		t.Fatalf("records=%+v", records)
	}

	// 跨知识库融合返回问答答案，游标读取返回条目本身。
	sources, err := service.Sources(ctx, identity.Organization.ID, []string{base.ID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{"退款", "退款入口"}})
	if err != nil || len(result.Records) == 0 || result.Records[0].DocumentID != refundID || result.Records[0].Answer == nil || *result.Records[0].Answer != answer {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, record := range result.Records[1:] {
		if record.DocumentID == refundID {
			t.Fatalf("refund entry duplicated across queries: %+v", result.Records)
		}
	}
	cursor := result.Records[0].Cursor
	window, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor, Before: 1, After: 1})
	if err != nil || len(window.Records) != 1 || window.Records[0].SegmentID != refundID || window.Records[0].Content != "如何退款？" || window.Records[0].Answer == nil || *window.Records[0].Answer != answer {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	// 条目修改并重新发布后，旧批次游标失效，新检索结果的游标读取当前答案。
	if _, err := knowledgeaction.NewSaveQAEntryAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, refundID, knowledgeaction.QAInput{Question: "如何退款？", Answer: "联系客服办理退款。"}); err != nil {
		t.Fatal(err)
	}
	task, _ := qaProcessInput(t, db, identity.Organization.ID, base.ID, refundID)
	if err := knowledgeaction.NewProcessQAEntryAction(db, probe).Execute(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor}); !errors.Is(err, knowledgeaction.ErrSegmentStale) {
		t.Fatalf("err=%v", err)
	}
	result, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{"退款"}})
	if err != nil || len(result.Records) == 0 || result.Records[0].DocumentID != refundID || result.Records[0].Cursor.SegmentBatchID == cursor.SegmentBatchID {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	cursor = result.Records[0].Cursor
	if window, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor}); err != nil || len(window.Records) != 1 || window.Records[0].Cursor != cursor || *window.Records[0].Answer != "联系客服办理退款。" {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	if err := knowledgeaction.NewDeleteQAEntryAction(db).Execute(ctx, identity, base.ID, refundID); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor}); !errors.Is(err, knowledgeaction.ErrSegmentStale) {
		t.Fatalf("err=%v", err)
	}
	records, err = service.Retrieve(ctx, identity, base.ID, "退款")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.DocumentID == refundID {
			t.Fatalf("deleted entry returned: %+v", records)
		}
	}
}

// TestKnowledgeSearchBatchesQueryEmbedding 验证跨知识库检索按向量模型配置分组，每组以一次调用向量化全部查询，向量化失败时仍返回词法命中。
func TestKnowledgeSearchBatchesQueryEmbedding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	installed, first := newDocumentFixture(t, db)
	identity := installed.Identity
	var baseIDs []string
	baseIDs = append(baseIDs, first.ID)
	// 第二个知识库与第一个共用向量模型配置，第三个使用独立供应商。
	shared := newKnowledgeBaseInput(t, db, identity, "共用向量", domain.KnowledgeBaseCategoryStandard)
	shared.EmbeddingProviderID, shared.RerankProviderID = first.EmbeddingProviderID, first.RerankProviderID
	for _, input := range []knowledgeaction.Input{shared, newKnowledgeBaseInput(t, db, identity, "独立向量", domain.KnowledgeBaseCategoryStandard)} {
		base, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, input)
		if err != nil {
			t.Fatal(err)
		}
		baseIDs = append(baseIDs, base.ID)
	}
	probe := &retrievalProbe{}
	refundID := publishRetrievalDocument(t, db, probe, identity, first, "退款政策.txt", "签收后七天内可以申请退款。")
	service := knowledgeaction.NewRetrievalService(db, probe, probe)
	for _, embedFail := range []bool{false, true} {
		probe.embedFail, probe.embedded = embedFail, nil
		sources, err := service.Sources(ctx, identity.Organization.ID, baseIDs)
		if err != nil {
			t.Fatal(err)
		}
		// 向量化失败时同组来源共用失败结果，词法路照常返回命中。
		result, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{"退款", "发票", "退款"}})
		if err != nil || len(result.Records) == 0 || result.Records[0].DocumentID != refundID {
			t.Fatalf("embedFail=%t result=%+v err=%v", embedFail, result, err)
		}
		if len(probe.embedded) != 2 {
			t.Fatalf("embedFail=%t embedded=%v", embedFail, probe.embedded)
		}
		for _, inputs := range probe.embedded {
			if !slices.Equal(inputs, []string{"退款", "发票"}) {
				t.Fatalf("embedFail=%t embedded=%v", embedFail, probe.embedded)
			}
		}
	}
}
