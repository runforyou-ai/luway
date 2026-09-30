// Package knowledgeretrieval 编排跨知识库关键词检索、结果融合和游标读取。
package knowledgeretrieval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/domain"
)

// Record 定义统一的知识库检索分段。
type Record struct {
	KnowledgeBaseID   string   `json:"knowledgeBaseId"`
	KnowledgeBaseName string   `json:"knowledgeBaseName"`
	DocumentID        string   `json:"documentId"`
	DocumentName      string   `json:"documentName"`
	SegmentID         string   `json:"segmentId"`
	SegmentBatchID    string   `json:"-"`
	Position          int      `json:"position"`
	Context           string   `json:"context,omitempty"`
	Content           string   `json:"content"`
	Answer            *string  `json:"answer"`
	Score             *float64 `json:"score,omitempty"`
	MatchedQueries    []int    `json:"matchedQueryIndexes,omitempty"`
	Cursor            Cursor   `json:"cursor"`
	Matched           bool     `json:"matched"`
}

// Cursor 定位知识库来源已发布批次中的一个分段。
type Cursor struct {
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	DocumentID      string `json:"documentId"`
	SegmentID       string `json:"segmentId"`
	SegmentBatchID  string `json:"segmentBatchId"`
	Position        int    `json:"position"`
}

// Request 定义关键词检索或游标检索参数。
type Request struct {
	Queries []string `json:"queries,omitempty"`
	Cursor  *Cursor  `json:"cursor,omitempty"`
	Before  int      `json:"before,omitempty"`
	After   int      `json:"after,omitempty"`
}

// Source 固定一次检索可访问的知识库及其单查询实现；Prepare 可为空，非空时在并发检索前接收本次全部查询。
type Source struct {
	ID, Name string
	Prepare  func(context.Context, []string)
	Retrieve func(context.Context, string) ([]Record, error)
	Read     func(context.Context, Cursor, int, int) ([]Record, error)
}

// Result 定义融合后的知识库检索结果。
type Result struct {
	Records []Record `json:"records"`
}

type completedSearch struct {
	sourceIndex, queryIndex int
	records                 []Record
	err                     error
}
type fusedRecord struct {
	record  Record
	score   float64
	matched map[int]struct{}
}

// Search 执行关键词检索或读取游标周边分段。
func Search(ctx context.Context, sources []Source, request Request) (Result, error) {
	startedAt := time.Now()
	if request.Cursor != nil {
		if len(request.Queries) > 0 {
			return Result{}, errors.New("knowledge query and cursor cannot be used together")
		}
		if request.Before < 0 || request.After < 0 {
			return Result{}, errors.New("knowledge cursor range is negative")
		}
		cursor := *request.Cursor
		cursor.KnowledgeBaseID = strings.TrimSpace(cursor.KnowledgeBaseID)
		cursor.DocumentID = strings.TrimSpace(cursor.DocumentID)
		cursor.SegmentID = strings.TrimSpace(cursor.SegmentID)
		cursor.SegmentBatchID = strings.TrimSpace(cursor.SegmentBatchID)
		if cursor.KnowledgeBaseID == "" || cursor.DocumentID == "" ||
			cursor.SegmentID == "" || cursor.SegmentBatchID == "" || cursor.Position <= 0 {
			return Result{}, errors.New("knowledge cursor is invalid")
		}
		for _, source := range sources {
			if source.ID != cursor.KnowledgeBaseID {
				continue
			}
			records, err := source.Read(ctx, cursor, request.Before, request.After)
			if err != nil {
				return Result{}, fmt.Errorf("read knowledge cursor: %w", err)
			}
			// 阅读结果同样带上知识库标识和自身游标，供模型继续向前后读取。
			for index := range records {
				records[index].KnowledgeBaseID, records[index].KnowledgeBaseName = source.ID, source.Name
				records[index].Cursor = Cursor{
					KnowledgeBaseID: source.ID, DocumentID: records[index].DocumentID,
					SegmentID: records[index].SegmentID, SegmentBatchID: records[index].SegmentBatchID, Position: records[index].Position,
				}
			}
			result := Result{Records: records}
			slog.Info("知识库游标查询成功",
				"knowledge_base_id", cursor.KnowledgeBaseID,
				"document_id", cursor.DocumentID,
				"result_count", len(records),
				"duration_ms", time.Since(startedAt).Milliseconds(),
			)
			return result, nil
		}
		return Result{}, errors.New("knowledge cursor is outside the search scope")
	}
	result, err := searchQueries(ctx, sources, request.Queries)
	if err == nil {
		slog.Info("知识库关键词查询成功",
			"knowledge_base_count", len(sources),
			"query_count", len(request.Queries),
			"result_count", len(result.Records),
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	}
	return result, err
}

// searchQueries 并发执行指定范围内的查询，并使用 RRF 融合命中分段。
func searchQueries(ctx context.Context, sources []Source, queries []string) (Result, error) {
	unique := make([]string, 0, len(queries))
	indexes := make(map[string][]int, len(queries))
	for index, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" {
			return Result{}, errors.New("knowledge search query is empty")
		}
		if utf8.RuneCountInString(query) > domain.KnowledgeRetrievalQueryMaxLength {
			return Result{}, errors.New("knowledge search query is too long")
		}
		if _, ok := indexes[query]; !ok {
			unique = append(unique, query)
		}
		indexes[query] = append(indexes[query], index+1)
	}
	if len(unique) == 0 {
		return Result{}, errors.New("knowledge search requires at least one query")
	}
	if len(sources) == 0 {
		return Result{Records: []Record{}}, nil
	}
	for _, source := range sources {
		if source.Prepare != nil {
			source.Prepare(ctx, unique)
		}
	}
	completed := make(chan completedSearch, len(sources)*len(unique))
	var group sync.WaitGroup
	for sourceIndex, source := range sources {
		for queryIndex, query := range unique {
			group.Add(1)
			go func() {
				defer group.Done()
				records, err := source.Retrieve(ctx, query)
				completed <- completedSearch{sourceIndex, queryIndex, records, err}
			}()
		}
	}
	group.Wait()
	close(completed)
	items := make([]completedSearch, 0, cap(completed))
	for item := range completed {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].sourceIndex != items[j].sourceIndex {
			return items[i].sourceIndex < items[j].sourceIndex
		}
		return items[i].queryIndex < items[j].queryIndex
	})
	fused := make(map[string]*fusedRecord)
	var succeeded bool
	var firstError error
	for _, item := range items {
		if item.err != nil {
			slog.Warn("知识库检索部分失败",
				"knowledge_base_id", sources[item.sourceIndex].ID,
				"query", unique[item.queryIndex],
				"error", item.err,
			)
			if firstError == nil {
				firstError = item.err
			}
			continue
		}
		succeeded = true
		source := sources[item.sourceIndex]
		for rank, record := range item.records {
			key := source.ID + ":" + record.SegmentID
			candidate := fused[key]
			if candidate == nil {
				record.KnowledgeBaseID, record.KnowledgeBaseName = source.ID, source.Name
				candidate = &fusedRecord{record: record, matched: map[int]struct{}{}}
				fused[key] = candidate
			}
			candidate.score += 1 / float64(61+rank)
			for _, index := range indexes[unique[item.queryIndex]] {
				candidate.matched[index] = struct{}{}
			}
		}
	}
	if !succeeded {
		return Result{}, fmt.Errorf("search knowledge: %w", firstError)
	}
	ordered := make([]*fusedRecord, 0, len(fused))
	for _, candidate := range fused {
		for index := range candidate.matched {
			candidate.record.MatchedQueries = append(candidate.record.MatchedQueries, index)
		}
		sort.Ints(candidate.record.MatchedQueries)
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		left := ordered[i].record.KnowledgeBaseID + ":" + ordered[i].record.SegmentID
		right := ordered[j].record.KnowledgeBaseID + ":" + ordered[j].record.SegmentID
		return left < right
	})
	result := Result{Records: make([]Record, 0, len(ordered))}
	for _, candidate := range ordered {
		candidate.record.Cursor = Cursor{
			KnowledgeBaseID: candidate.record.KnowledgeBaseID,
			DocumentID:      candidate.record.DocumentID,
			SegmentID:       candidate.record.SegmentID, SegmentBatchID: candidate.record.SegmentBatchID, Position: candidate.record.Position,
		}
		candidate.record.Matched = true
		result.Records = append(result.Records, candidate.record)
	}
	return result, nil
}
