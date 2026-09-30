//go:build server

package knowledgebase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/knowledgeretrieval"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/embedding"
	"github.com/runforyou-ai/luway/pkg/rerank"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/runforyou-ai/luway/pkg/textsplit"
	"github.com/uptrace/bun"
)

// rrfConstant 是名次倒数融合的平滑常数。
const rrfConstant = 60

var (
	// ErrRetrievalNotReady 表示知识库还没有已发布的分段。
	ErrRetrievalNotReady = errors.New("knowledge base has no published segments")
	// ErrRetrievalQueryInvalid 表示检索内容为空或超出长度。
	ErrRetrievalQueryInvalid = errors.New("knowledge retrieval query is invalid")
)

type queryEmbedder interface {
	Embed(context.Context, embedding.Credential, string, int, []string) ([][]float32, error)
}
type candidateReranker interface {
	Rerank(context.Context, rerank.Credential, string, string, []string, int) ([]rerank.Score, error)
}

// RetrievalRecord 定义混合召回返回的一条来源片段、重排得分和两路名次。
// 文档记录指向命中的分段；问答记录以条目编号作为文档与分段编号，位置固定为 1，并携带完整答案。
type RetrievalRecord struct {
	DocumentID     string
	DocumentName   string
	SegmentID      string
	SegmentBatchID string
	Position       int
	Context        string
	Content        string
	Answer         string
	Score          float64
	LexicalRank    int
	VectorRank     int
}

// RetrievalService 对知识库执行词法与向量并行召回、名次融合、重排打分和相关性阈值过滤，人工检索测试与 Agent 共用。
type RetrievalService struct {
	db       *bun.DB
	embedder queryEmbedder
	reranker candidateReranker
}

// NewRetrievalService 创建知识库检索服务。
func NewRetrievalService(db *bun.DB, embedder queryEmbedder, reranker candidateReranker) *RetrievalService {
	return &RetrievalService{db: db, embedder: embedder, reranker: reranker}
}

// knowledgeSource 固定一个知识库及其模型凭据，承担该库的召回与阅读；同一向量模型配置的来源共用 embeddings；articlesOnly 为真时只召回帮助中心文章。
type knowledgeSource struct {
	service      *RetrievalService
	base         servermodels.KnowledgeBase
	embeddings   *queryEmbeddings
	rerank       rerank.Credential
	articlesOnly bool
}

// Retrieve 在当前企业的指定知识库中检索单条内容，用于知识库页面的检索测试。
func (s *RetrievalService) Retrieve(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID, query string) ([]RetrievalRecord, error) {
	query = strings.TrimSpace(query)
	if query == "" || utf8.RuneCountInString(query) > domain.KnowledgeRetrievalQueryMaxLength {
		return nil, ErrRetrievalQueryInvalid
	}
	sources, err := s.sources(ctx, identity.Organization.ID, []string{knowledgeBaseID})
	if err != nil {
		return nil, err
	}
	// 按知识库类别检查是否存在已发布批次的来源。
	ready := s.db.NewSelect().Model((*servermodels.KnowledgeDocument)(nil)).Where("kd.knowledge_base_id = ? AND kd.segment_batch_id IS NOT NULL", knowledgeBaseID)
	if sources[0].base.Category == string(domain.KnowledgeBaseCategoryQA) {
		ready = s.db.NewSelect().Model((*servermodels.KnowledgeQAEntry)(nil)).Where("kqe.knowledge_base_id = ? AND kqe.segment_batch_id IS NOT NULL", knowledgeBaseID)
	}
	published, err := ready.Exists(ctx)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, ErrRetrievalNotReady
	}
	return sources[0].retrieve(ctx, query)
}

// Sources 按企业校验知识库并构造检索来源，供多知识库融合检索使用。
func (s *RetrievalService) Sources(ctx context.Context, organizationID string, knowledgeBaseIDs []string) ([]knowledgeretrieval.Source, error) {
	sources, err := s.sources(ctx, organizationID, knowledgeBaseIDs)
	if err != nil {
		return nil, err
	}
	return retrievalSources(sources), nil
}

// ArticleSources 按企业校验知识库并构造只召回帮助中心文章的来源：文档知识库只召回在线编写的文档，问答知识库召回全部条目。
func (s *RetrievalService) ArticleSources(ctx context.Context, organizationID string, knowledgeBaseIDs []string) ([]knowledgeretrieval.Source, error) {
	sources, err := s.sources(ctx, organizationID, knowledgeBaseIDs)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		source.articlesOnly = true
	}
	return retrievalSources(sources), nil
}

// retrievalSources 把知识库来源转换为跨知识库融合检索使用的来源。
func retrievalSources(sources []*knowledgeSource) []knowledgeretrieval.Source {
	output := make([]knowledgeretrieval.Source, 0, len(sources))
	for _, source := range sources {
		output = append(output, knowledgeretrieval.Source{
			ID: source.base.ID, Name: source.base.Name,
			Prepare: func(ctx context.Context, queries []string) {
				source.embeddings.submit(ctx, queries)
			},
			Retrieve: func(ctx context.Context, query string) ([]knowledgeretrieval.Record, error) {
				records, err := source.retrieve(ctx, query)
				return retrievalRecords(records, true), err
			},
			Read: func(ctx context.Context, cursor knowledgeretrieval.Cursor, before, after int) ([]knowledgeretrieval.Record, error) {
				records, err := source.read(ctx, cursor.DocumentID, cursor.SegmentID, cursor.SegmentBatchID, before, after)
				return retrievalRecords(records, false), err
			},
		})
	}
	return output
}

// sources 读取同企业知识库及其向量、重排模型凭据；任一知识库不存在时返回 ErrNotFound。
func (s *RetrievalService) sources(ctx context.Context, organizationID string, knowledgeBaseIDs []string) ([]*knowledgeSource, error) {
	for _, id := range knowledgeBaseIDs {
		if !common.ValidUUID(id) {
			return nil, ErrNotFound
		}
	}
	var bases []servermodels.KnowledgeBase
	if err := s.db.NewSelect().Model(&bases).Where("kb.organization_id = ? AND kb.id IN (?)", organizationID, bun.In(knowledgeBaseIDs)).Scan(ctx); err != nil {
		return nil, err
	}
	if len(bases) != len(knowledgeBaseIDs) {
		return nil, ErrNotFound
	}
	providerIDs := make([]string, 0, len(bases)*2)
	for _, base := range bases {
		providerIDs = append(providerIDs, base.EmbeddingProviderID, base.RerankProviderID)
	}
	var providers []servermodels.AIProvider
	if err := s.db.NewSelect().Model(&providers).Where("aip.organization_id = ? AND aip.id IN (?)", organizationID, bun.In(providerIDs)).Scan(ctx); err != nil {
		return nil, err
	}
	byID := make(map[string]servermodels.AIProvider, len(providers))
	for _, provider := range providers {
		byID[provider.ID] = provider
	}
	sources := make([]*knowledgeSource, 0, len(bases))
	embeddings := make(map[queryEmbeddingKey]*queryEmbeddings, len(bases))
	for _, id := range knowledgeBaseIDs {
		for _, base := range bases {
			if base.ID != id {
				continue
			}
			source := &knowledgeSource{service: s, base: base}
			provider, ok := byID[base.EmbeddingProviderID]
			if !ok {
				return nil, &embedding.Error{Code: "embedding_model_unavailable"}
			}
			key := queryEmbeddingKey{providerID: provider.ID, model: base.EmbeddingModelIdentifier, dimension: base.EmbeddingDimension}
			if source.embeddings = embeddings[key]; source.embeddings == nil {
				credential, err := embeddingCredential(&provider)
				if err != nil {
					return nil, err
				}
				source.embeddings = newQueryEmbeddings(s.embedder, credential, key)
				embeddings[key] = source.embeddings
			}
			if provider, ok = byID[base.RerankProviderID]; !ok {
				return nil, &rerank.Error{Code: "rerank_model_unavailable"}
			}
			rerankBaseURL, err := modelprovider.CompatibleBaseURL(provider.Brand, provider.APIURL)
			if err != nil {
				return nil, &rerank.Error{Code: "rerank_model_unavailable"}
			}
			// 阿里云使用 DashScope 原生重排接口，其余品牌使用通用的 rerank 接口格式。
			protocol := rerank.ProtocolCompatible
			if provider.Brand == string(domain.AIProviderBrandAlibaba) {
				protocol = rerank.ProtocolDashScope
			}
			source.rerank = rerank.Credential{Protocol: protocol, BaseURL: rerankBaseURL, APIKey: provider.APIKey}
			sources = append(sources, source)
		}
	}
	return sources, nil
}

// retrieve 并行执行向量路和词法路，按名次倒数融合选出候选，交给重排模型打分，去掉低于相关性阈值的候选后按得分截取知识库的召回数量。
func (k *knowledgeSource) retrieve(ctx context.Context, query string) ([]RetrievalRecord, error) {
	started := time.Now()
	tsquery, lexical := searchtext.WordQuery(query)
	var vectorHits, lexicalHits []segmentHit
	var vectorErr, lexicalErr error
	var group sync.WaitGroup
	group.Go(func() {
		vector, err := k.embeddings.vector(ctx, query)
		if err != nil {
			vectorErr = err
			return
		}
		vectorHits, vectorErr = searchSegmentsByVector(ctx, k.service.db, k.base, k.articlesOnly, vector)
	})
	if lexical {
		group.Go(func() {
			lexicalHits, lexicalErr = searchSegmentsByText(ctx, k.service.db, k.base, k.articlesOnly, tsquery)
		})
	}
	group.Wait()
	if vectorErr != nil && (lexicalErr != nil || !lexical) {
		return nil, fmt.Errorf("retrieve knowledge base: %w", vectorErr)
	}
	if vectorErr != nil {
		slog.Warn("知识库向量召回失败", "knowledge_base_id", k.base.ID, "error", vectorErr)
	}
	if lexicalErr != nil {
		slog.Warn("知识库词法召回失败", "knowledge_base_id", k.base.ID, "error", lexicalErr)
	}
	type candidate struct {
		hit                     segmentHit
		score                   float64
		reranked                bool
		lexicalRank, vectorRank int
	}
	fused := map[string]*candidate{}
	ordered := make([]*candidate, 0, len(lexicalHits)+len(vectorHits))
	for rank, hit := range lexicalHits {
		item := &candidate{hit: hit, lexicalRank: rank + 1}
		fused[hit.ID] = item
		ordered = append(ordered, item)
	}
	for rank, hit := range vectorHits {
		item := fused[hit.ID]
		if item == nil {
			item = &candidate{hit: hit}
			fused[hit.ID] = item
			ordered = append(ordered, item)
		}
		item.vectorRank = rank + 1
	}
	for _, item := range ordered {
		if item.lexicalRank > 0 {
			item.score += 1 / float64(rrfConstant+item.lexicalRank)
		}
		if item.vectorRank > 0 {
			item.score += 1 / float64(rrfConstant+item.vectorRank)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		return ordered[i].hit.ID < ordered[j].hit.ID
	})
	// 最终分数与顺序只由重排得分决定，供应商未返回得分和重排得分低于相关性阈值的候选不进入结果。
	belowThreshold := 0
	if len(ordered) > 0 {
		documents := make([]string, 0, len(ordered))
		for _, item := range ordered {
			documents = append(documents, textsplit.IndexText(item.hit.Context, item.hit.Content))
		}
		scores, err := k.service.reranker.Rerank(ctx, k.rerank, k.base.RerankModelIdentifier, query, documents, len(documents))
		if err != nil {
			return nil, fmt.Errorf("rerank knowledge candidates: %w", err)
		}
		reranked := make([]*candidate, 0, len(scores))
		for _, score := range scores {
			if item := ordered[score.Index]; !item.reranked {
				item.score, item.reranked = score.Relevance, true
				reranked = append(reranked, item)
			}
		}
		if len(reranked) < len(ordered) {
			slog.Warn("重排模型未返回全部候选得分", "knowledge_base_id", k.base.ID, "candidate_count", len(ordered), "scored_count", len(reranked))
		}
		sort.SliceStable(reranked, func(i, j int) bool { return reranked[i].score > reranked[j].score })
		qualified := 0
		for qualified < len(reranked) && reranked[qualified].score >= k.base.RetrievalScoreThreshold {
			qualified++
		}
		belowThreshold = len(reranked) - qualified
		ordered = reranked[:qualified]
	}
	// 问答库按条目折叠，保留每个条目得分最高的片段。
	qa := k.base.Category == string(domain.KnowledgeBaseCategoryQA)
	if qa {
		seen := make(map[string]bool, len(ordered))
		folded := make([]*candidate, 0, len(ordered))
		for _, item := range ordered {
			if !seen[item.hit.SourceID] {
				seen[item.hit.SourceID] = true
				folded = append(folded, item)
			}
		}
		ordered = folded
	}
	ordered = ordered[:min(len(ordered), k.base.RetrievalCount)]
	records := make([]RetrievalRecord, 0, len(ordered))
	for _, item := range ordered {
		record := RetrievalRecord{
			DocumentID: item.hit.SourceID, DocumentName: item.hit.SourceName,
			SegmentID: item.hit.ID, SegmentBatchID: item.hit.SegmentBatchID, Position: item.hit.Position,
			Context: item.hit.Context, Content: item.hit.Content, Score: item.score, LexicalRank: item.lexicalRank, VectorRank: item.vectorRank,
		}
		// 问答记录以条目编号作为分段编号，位置固定为 1。
		if qa {
			record.SegmentID, record.Position = item.hit.SourceID, 1
		}
		records = append(records, record)
	}
	if qa {
		if err := attachQAAnswers(ctx, k.service.db, records); err != nil {
			return nil, err
		}
	}
	slog.Info("知识库混合召回完成",
		"knowledge_base_id", k.base.ID, "lexical_count", len(lexicalHits), "vector_count", len(vectorHits),
		"score_threshold", k.base.RetrievalScoreThreshold, "below_threshold_count", belowThreshold,
		"result_count", len(records), "duration_ms", time.Since(started).Milliseconds())
	return records, nil
}

// read 读取游标指向的内容，游标批次必须是来源当前已发布批次：文档返回分段及前后相邻分段，问答返回条目本身。
func (k *knowledgeSource) read(ctx context.Context, sourceID, segmentID, batchID string, before, after int) ([]RetrievalRecord, error) {
	if k.base.Category == string(domain.KnowledgeBaseCategoryQA) {
		if !common.ValidUUID(sourceID) || sourceID != segmentID || !common.ValidUUID(batchID) {
			return nil, ErrSegmentStale
		}
		var entry struct {
			Question string `bun:"question"`
			Answer   string `bun:"answer"`
		}
		err := k.service.db.NewSelect().TableExpr("knowledge_qa_entries AS kqe").ColumnExpr("question.content AS question, answer.content AS answer").
			Join("JOIN knowledge_qa_contents question ON question.entry_id = kqe.id AND question.kind = ?", domain.KnowledgeQAContentPrimaryQuestion).
			Join("JOIN knowledge_qa_contents answer ON answer.entry_id = kqe.id AND answer.kind = ?", domain.KnowledgeQAContentAnswer).
			Where("kqe.id = ? AND kqe.knowledge_base_id = ? AND kqe.segment_batch_id = ?", sourceID, k.base.ID, batchID).Scan(ctx, &entry)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSegmentStale
		}
		if err != nil {
			return nil, err
		}
		return []RetrievalRecord{{DocumentID: sourceID, DocumentName: entry.Question, SegmentID: sourceID, SegmentBatchID: batchID, Position: 1, Content: entry.Question, Answer: entry.Answer}}, nil
	}
	hits, err := readSegmentWindow(ctx, k.service.db, k.base, sourceID, segmentID, batchID, before, after)
	if err != nil {
		return nil, err
	}
	records := make([]RetrievalRecord, 0, len(hits))
	for _, hit := range hits {
		records = append(records, RetrievalRecord{DocumentID: hit.SourceID, DocumentName: hit.SourceName, SegmentID: hit.ID, SegmentBatchID: hit.SegmentBatchID, Position: hit.Position, Context: hit.Context, Content: hit.Content})
	}
	return records, nil
}

// attachQAAnswers 为问答记录补上条目当前的完整答案。
func attachQAAnswers(ctx context.Context, db bun.IDB, records []RetrievalRecord) error {
	if len(records) == 0 {
		return nil
	}
	entryIDs := make([]string, 0, len(records))
	for _, record := range records {
		entryIDs = append(entryIDs, record.DocumentID)
	}
	var answers []servermodels.KnowledgeQAContent
	if err := db.NewSelect().Model(&answers).Column("entry_id", "content").Where("kqc.entry_id IN (?) AND kqc.kind = ?", bun.In(entryIDs), domain.KnowledgeQAContentAnswer).Scan(ctx); err != nil {
		return err
	}
	byEntry := make(map[string]string, len(answers))
	for _, answer := range answers {
		byEntry[answer.EntryID] = answer.Content
	}
	for index := range records {
		records[index].Answer = byEntry[records[index].DocumentID]
	}
	return nil
}

// retrievalRecords 把召回或阅读结果映射为跨知识库融合使用的统一记录，只有召回结果携带分数。
func retrievalRecords(records []RetrievalRecord, scored bool) []knowledgeretrieval.Record {
	output := make([]knowledgeretrieval.Record, 0, len(records))
	for _, record := range records {
		item := knowledgeretrieval.Record{
			DocumentID: record.DocumentID, DocumentName: record.DocumentName,
			SegmentID: record.SegmentID, SegmentBatchID: record.SegmentBatchID, Position: record.Position,
			Context: record.Context, Content: record.Content,
		}
		if record.Answer != "" {
			answer := record.Answer
			item.Answer = &answer
		}
		if scored {
			score := record.Score
			item.Score = &score
		}
		output = append(output, item)
	}
	return output
}
