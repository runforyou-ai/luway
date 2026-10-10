//go:build server

package integrationtest

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/runforyou-ai/einorun/provider/apierr"
	"github.com/runforyou-ai/einorun/provider/embedding"
	"github.com/runforyou-ai/einorun/provider/rerank"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/knowledgeretrieval"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// retrievalTopics 按关键词把文本映射到向量分量，让向量路只在同主题文本之间距离为零。
var retrievalTopics = []string{"退款", "发票", "配送"}

// retrievalProbe 是知识库检索测试的网页、向量与重排替身。
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
func (p *retrievalProbe) Convert(context.Context, string, string, io.Reader) (string, error) {
	return p.markdown, nil
}

// Embed 按文本包含的主题关键词生成单位向量，未命中主题的文本落在独立分量。
func (p *retrievalProbe) Embed(_ context.Context, _ embedding.Endpoint, _ string, dimension int, inputs []string) (embedding.Result, error) {
	p.embedMu.Lock()
	p.embedded = append(p.embedded, slices.Clone(inputs))
	p.embedMu.Unlock()
	if p.embedFail {
		return embedding.Result{}, apierr.FromStatus(http.StatusInternalServerError, "")
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
	return embedding.Result{Vectors: vectors, InputTokens: len(inputs)}, nil
}

// Rerank 记录调用次数，按候选是否包含退款、发票关键词给出固定相关性。
func (p *retrievalProbe) Rerank(_ context.Context, _ rerank.Endpoint, _, _ string, documents []string, _ int) (rerank.Result, error) {
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
	return rerank.Result{Scores: scores}, nil
}

// publishRetrievalDocument 上传并处理一篇文档，返回文档编号。
func publishRetrievalDocument(t *testing.T, db *bun.DB, probe *retrievalProbe, identity *servermodels.Identity, base *knowledgeaction.Record, name, markdown string) string {
	t.Helper()
	ctx := context.Background()
	file := uploadedDocumentFile(t, db, identity, name)
	documents, err := knowledgeaction.NewCreateDocumentsAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, []string{file.ID})
	require.NoError(t, err)
	var document servermodels.KnowledgeDocument
	require.NoError(t, db.NewSelect().Model(&document).Where("kd.id = ?", documents[0].ID).Scan(ctx))
	probe.markdown = markdown
	worker := knowledgeaction.NewProcessDocumentAction(db, probe, modelcall.New(db, modelcall.Upstreams{Embedder: probe}, nil), probe, probe)
	err = worker.Execute(ctx, knowledgeaction.ProcessInput{
		WorkspaceID: identity.Workspace.ID, KnowledgeBaseID: base.ID, DocumentID: document.ID, ProcessingID: document.ProcessingID,
		ChunkLength: document.ChunkLength, ChunkOverlap: document.ChunkOverlap,
		EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension,
	})
	require.NoError(t, err)
	return document.ID
}

// TestKnowledgeHybridRetrieval 验证词法与向量两路召回、名次融合、重排得分、相关性阈值过滤、单路失败保留、企业隔离与游标阅读。
func TestKnowledgeHybridRetrieval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	identity := installed.Identity
	probe := &retrievalProbe{}
	service := knowledgeaction.NewRetrievalService(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe, Reranker: probe}, nil))

	_, err = service.Retrieve(ctx, identity, base.ID, "退款")
	require.ErrorIs(t, err, knowledgeaction.ErrRetrievalNotReady)
	refundID := publishRetrievalDocument(t, db, probe, identity, base, "退款政策.txt", strings.Repeat("签收后七天内可以申请退款，退款金额原路返回。", 30))
	invoiceID := publishRetrievalDocument(t, db, probe, identity, base, "发票说明.txt", "下单时可以选择开具电子发票。")
	deliveryID := publishRetrievalDocument(t, db, probe, identity, base, "配送说明.txt", "配送时效按收货地址计算。")

	// 词法路与向量路都命中退款文档，最终分数为重排得分。
	records, err := service.Retrieve(ctx, identity, base.ID, "如何申请退款")
	require.NoError(t, err)
	require.NotEmpty(t, records)
	require.Equal(t, refundID, records[0].DocumentID)
	require.Equal(t, "退款政策.txt", records[0].DocumentName)
	require.NotZero(t, records[0].LexicalRank)
	require.NotZero(t, records[0].VectorRank)
	require.Equal(t, 1.0, records[0].Score)
	require.NotEmpty(t, records[0].SegmentBatchID)
	require.NotZero(t, records[0].Position)
	require.LessOrEqual(t, len(records), base.RetrievalCount)
	require.Equal(t, 1, probe.reranked)
	// 两路都把发票文档排在首位，重排后退款文档仍按得分排在前面。
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(records), 2)
	require.Equal(t, refundID, records[0].DocumentID)
	require.Zero(t, records[0].LexicalRank)
	for _, record := range records {
		require.GreaterOrEqual(t, record.Score, base.RetrievalScoreThreshold, "below threshold record=%+v", record)
	}

	// 多知识库来源经统一融合返回，并可按游标读取相邻分段。
	sources, err := service.Sources(ctx, modelcall.MemberScope(identity, domain.AIModelCallSourceKnowledgeBase, ""), []string{base.ID})
	require.NoError(t, err)
	result, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{"退款", "退款金额"}})
	require.NoError(t, err)
	require.NotEmpty(t, result.Records)
	require.Equal(t, base.ID, result.Records[0].KnowledgeBaseID)
	require.Equal(t, refundID, result.Records[0].DocumentID)
	var cursor knowledgeretrieval.Cursor
	for _, record := range result.Records {
		if record.DocumentID == refundID && record.Position == 1 {
			cursor = record.Cursor
		}
	}
	window, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor, After: 1})
	require.NoError(t, err)
	require.Len(t, window.Records, 2)
	require.Equal(t, cursor.SegmentID, window.Records[0].SegmentID)
	require.Equal(t, 2, window.Records[1].Position)

	// 文档重新发布后，旧批次游标失效，新检索结果的游标可读取相邻分段。
	require.NoError(t, knowledgeaction.NewDocumentProcessing(db, newKnowledgeTasks(t, db)).Retry(ctx, identity, base.ID, refundID))
	var republished servermodels.KnowledgeDocument
	require.NoError(t, db.NewSelect().Model(&republished).Where("kd.id = ?", refundID).Scan(ctx))
	probe.markdown = strings.Repeat("签收后七天内可以申请退款，退款金额原路返回。", 30)
	require.NoError(t, knowledgeaction.NewProcessDocumentAction(db, probe, modelcall.New(db, modelcall.Upstreams{Embedder: probe}, nil), probe, probe).Execute(ctx, knowledgeaction.ProcessInput{
		WorkspaceID: identity.Workspace.ID, KnowledgeBaseID: base.ID, DocumentID: refundID, ProcessingID: republished.ProcessingID,
		ChunkLength: republished.ChunkLength, ChunkOverlap: republished.ChunkOverlap,
		EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension,
	}))
	_, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor, After: 1})
	require.ErrorIs(t, err, knowledgeaction.ErrSegmentStale)
	stale := cursor
	result, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{"退款", "退款金额"}})
	require.NoError(t, err)
	for _, record := range result.Records {
		if record.DocumentID == refundID && record.Position == 1 {
			cursor = record.Cursor
		}
	}
	require.NotEqual(t, stale.SegmentBatchID, cursor.SegmentBatchID)
	window, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor, After: 1})
	require.NoError(t, err)
	require.Len(t, window.Records, 2)
	require.Equal(t, cursor.SegmentID, window.Records[0].SegmentID)
	require.Equal(t, cursor.SegmentBatchID, window.Records[1].Cursor.SegmentBatchID)

	// 向量路失败时保留词法路结果。
	probe.embedFail = true
	records, err = service.Retrieve(ctx, identity, base.ID, "退款")
	require.NoError(t, err)
	require.NotEmpty(t, records)
	require.Equal(t, refundID, records[0].DocumentID)
	require.Zero(t, records[0].VectorRank)
	require.Equal(t, 1, records[0].LexicalRank)
	// 没有词法词元时向量路失败即整体失败。
	_, err = service.Retrieve(ctx, identity, base.ID, "什么")
	require.Error(t, err, "expected embedding failure")
	probe.embedFail = false

	// 召回数量限制作用于重排后的结果。
	input := newKnowledgeBaseInput(t, db, identity, base.Name, base.Category)
	input.EmbeddingModelID, input.RerankModelID, input.RetrievalCount = base.EmbeddingModelID, base.RerankModelID, 2
	_, err = knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, input)
	require.NoError(t, err)
	records, err = service.Retrieve(ctx, identity, base.ID, "如何申请退款")
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, refundID, records[0].DocumentID)
	require.Equal(t, refundID, records[1].DocumentID)

	// 相关性阈值为 0 时保留低分候选。
	input.RetrievalCount, input.RetrievalScoreThreshold = 20, 0
	_, err = knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, input)
	require.NoError(t, err)
	records, err = service.Retrieve(ctx, identity, base.ID, "配送")
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(records, func(record knowledgeaction.RetrievalRecord) bool { return record.DocumentID == deliveryID }), "records=%+v", records)
	// 重排得分等于相关性阈值的候选仍返回。
	input.RetrievalScoreThreshold = 0.8
	_, err = knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, input)
	require.NoError(t, err)
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(records, func(record knowledgeaction.RetrievalRecord) bool { return record.DocumentID == invoiceID }), "records=%+v", records)
	require.False(t, slices.ContainsFunc(records, func(record knowledgeaction.RetrievalRecord) bool { return record.DocumentID == deliveryID }), "records=%+v", records)
	// 提高阈值后只返回重排得分达到阈值的候选。
	input.RetrievalScoreThreshold = 0.9
	_, err = knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, input)
	require.NoError(t, err)
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, refundID, records[0].DocumentID)
	require.Equal(t, refundID, records[1].DocumentID)

	other, _ := newDocumentFixture(t, db)
	_, err = service.Retrieve(ctx, other.Identity, base.ID, "退款")
	require.ErrorIs(t, err, knowledgeaction.ErrNotFound)

	// 文档删除后旧游标不可读取。
	require.NoError(t, knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, identity, base.ID, refundID))
	_, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor})
	require.ErrorIs(t, err, knowledgeaction.ErrSegmentStale)
	// 剩余候选全部低于相关性阈值时返回空结果。
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	require.NoError(t, err)
	require.Empty(t, records)
}

// publishQAEntry 保存一条问答并执行索引任务，返回条目编号。
func publishQAEntry(t *testing.T, db *bun.DB, probe *retrievalProbe, identity *servermodels.Identity, base *knowledgeaction.Record, input knowledgeaction.QAInput) string {
	t.Helper()
	ctx := context.Background()
	entry, err := knowledgeaction.NewSaveQAEntryAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, "", input)
	require.NoError(t, err)
	task, _ := qaProcessInput(t, db, identity.Workspace.ID, base.ID, entry.ID)
	require.NoError(t, knowledgeaction.NewProcessQAEntryAction(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe}, nil)).Execute(ctx, task))
	return entry.ID
}

// TestKnowledgeQARetrieval 验证问答库的就绪判断、问题与答案片段命中折叠为条目、低于相关性阈值的条目不返回、完整答案交付以及游标读取与失效。
func TestKnowledgeQARetrieval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	identity, base := newQAFixture(t, db)
	probe := &retrievalProbe{}
	service := knowledgeaction.NewRetrievalService(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe, Reranker: probe}, nil))
	_, err = service.Retrieve(ctx, identity, base.ID, "退款")
	require.ErrorIs(t, err, knowledgeaction.ErrRetrievalNotReady)
	answer := strings.Repeat("进入订单详情申请退款，审核通过后退款原路返回。", 40)
	refundID := publishQAEntry(t, db, probe, identity, base, knowledgeaction.QAInput{Question: "如何退款？", Answer: answer, SimilarQuestions: []knowledgeaction.QASimilarQuestion{{Content: "退款入口在哪里"}, {Content: "怎么申请退款"}}})
	invoiceID := publishQAEntry(t, db, probe, identity, base, knowledgeaction.QAInput{Question: "怎么开发票", Answer: "下单时选择电子发票。"})
	deliveryID := publishQAEntry(t, db, probe, identity, base, knowledgeaction.QAInput{Question: "配送要多久", Answer: "配送时效按收货地址计算。"})

	// 主问题、相似问题和多段答案都命中退款条目，折叠后只返回一条并携带完整答案。
	records, err := service.Retrieve(ctx, identity, base.ID, "怎么申请退款")
	require.NoError(t, err)
	require.NotEmpty(t, records)
	first := records[0]
	require.Equal(t, struct {
		DocumentID, SegmentID string
		Position              int
		DocumentName, Answer  string
		Score                 float64
	}{refundID, refundID, 1, "如何退款？", answer, 1}, struct {
		DocumentID, SegmentID string
		Position              int
		DocumentName, Answer  string
		Score                 float64
	}{first.DocumentID, first.SegmentID, first.Position, first.DocumentName, first.Answer, first.Score})
	require.NotZero(t, first.LexicalRank)
	require.NotZero(t, first.VectorRank)
	require.Contains(t, first.Content, "退款")
	for _, record := range records[1:] {
		require.NotEqual(t, refundID, record.DocumentID, "refund entry not folded: %+v", records)
		require.NotEmpty(t, record.Answer, "record=%+v", record)
		require.Equal(t, record.DocumentID, record.SegmentID, "record=%+v", record)
	}
	require.False(t, slices.ContainsFunc(records, func(record knowledgeaction.RetrievalRecord) bool { return record.DocumentID == deliveryID }), "below threshold entry returned: %+v", records)
	require.LessOrEqual(t, len(records), base.RetrievalCount)
	// 发票查询命中发票条目并返回其答案。
	records, err = service.Retrieve(ctx, identity, base.ID, "发票")
	require.NoError(t, err)
	found := false
	for _, record := range records {
		if record.DocumentID == invoiceID {
			found = record.Answer == "下单时选择电子发票。" && record.DocumentName == "怎么开发票"
		}
	}
	require.True(t, found, "records=%+v", records)

	// 跨知识库融合返回问答答案，游标读取返回条目本身。
	sources, err := service.Sources(ctx, modelcall.MemberScope(identity, domain.AIModelCallSourceKnowledgeBase, ""), []string{base.ID})
	require.NoError(t, err)
	result, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{"退款", "退款入口"}})
	require.NoError(t, err)
	require.NotEmpty(t, result.Records)
	require.Equal(t, refundID, result.Records[0].DocumentID)
	require.NotNil(t, result.Records[0].Answer)
	require.Equal(t, answer, *result.Records[0].Answer)
	for _, record := range result.Records[1:] {
		require.NotEqual(t, refundID, record.DocumentID, "refund entry duplicated across queries: %+v", result.Records)
	}
	cursor := result.Records[0].Cursor
	window, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor, Before: 1, After: 1})
	require.NoError(t, err)
	require.Len(t, window.Records, 1)
	require.Equal(t, refundID, window.Records[0].SegmentID)
	require.Equal(t, "如何退款？", window.Records[0].Content)
	require.NotNil(t, window.Records[0].Answer)
	require.Equal(t, answer, *window.Records[0].Answer)
	// 条目修改并重新发布后，旧批次游标失效，新检索结果的游标读取当前答案。
	_, err = knowledgeaction.NewSaveQAEntryAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, refundID, knowledgeaction.QAInput{Question: "如何退款？", Answer: "联系客服办理退款。"})
	require.NoError(t, err)
	task, _ := qaProcessInput(t, db, identity.Workspace.ID, base.ID, refundID)
	require.NoError(t, knowledgeaction.NewProcessQAEntryAction(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe}, nil)).Execute(ctx, task))
	_, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor})
	require.ErrorIs(t, err, knowledgeaction.ErrSegmentStale)
	result, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{"退款"}})
	require.NoError(t, err)
	require.NotEmpty(t, result.Records)
	require.Equal(t, refundID, result.Records[0].DocumentID)
	require.NotEqual(t, cursor.SegmentBatchID, result.Records[0].Cursor.SegmentBatchID)
	cursor = result.Records[0].Cursor
	window, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor})
	require.NoError(t, err)
	require.Len(t, window.Records, 1)
	require.Equal(t, cursor, window.Records[0].Cursor)
	require.Equal(t, "联系客服办理退款。", *window.Records[0].Answer)
	require.NoError(t, knowledgeaction.NewDeleteQAEntryAction(db).Execute(ctx, identity, base.ID, refundID))
	_, err = knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Cursor: &cursor})
	require.ErrorIs(t, err, knowledgeaction.ErrSegmentStale)
	records, err = service.Retrieve(ctx, identity, base.ID, "退款")
	require.NoError(t, err)
	for _, record := range records {
		require.NotEqual(t, refundID, record.DocumentID, "deleted entry returned: %+v", records)
	}
}
