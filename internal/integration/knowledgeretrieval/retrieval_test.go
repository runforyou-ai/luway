package knowledgeretrieval

import (
	"context"
	"errors"
	"testing"
)

// TestSearchFusesMultipleSourcesAndQueries 验证多库多查询会去重并按 RRF 稳定融合。
func TestSearchFusesMultipleSourcesAndQueries(t *testing.T) {
	sources := []Source{
		{ID: "base-a", Name: "手册", Retrieve: func(_ context.Context, query string) ([]Record, error) {
			if query == "休假" {
				return []Record{{SegmentID: "shared", Content: "共同结果"}, {SegmentID: "leave", Content: "休假结果"}}, nil
			}
			return []Record{{SegmentID: "shared", Content: "共同结果"}}, nil
		}},
		{ID: "base-b", Name: "制度", Retrieve: func(context.Context, string) ([]Record, error) {
			return []Record{{SegmentID: "expense", Content: "报销结果"}}, nil
		}},
	}
	result, err := Search(context.Background(), sources, Request{Queries: []string{"休假", "报销", "休假"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 3 || result.Records[0].SegmentID != "shared" {
		t.Fatalf("records = %#v", result.Records)
	}
	if indexes := result.Records[0].MatchedQueries; len(indexes) != 3 || indexes[0] != 1 || indexes[1] != 2 || indexes[2] != 3 {
		t.Fatalf("matched query indexes = %#v", indexes)
	}
}

// TestSearchKeepsSuccessfulSources 验证部分来源失败时仍返回其他来源的结果。
func TestSearchKeepsSuccessfulSources(t *testing.T) {
	sources := []Source{
		{ID: "failed", Retrieve: func(context.Context, string) ([]Record, error) { return nil, errors.New("failed") }},
		{ID: "available", Retrieve: func(context.Context, string) ([]Record, error) { return []Record{{SegmentID: "result"}}, nil }},
	}
	result, err := Search(context.Background(), sources, Request{Queries: []string{"查询"}})
	if err != nil || len(result.Records) != 1 || result.Records[0].KnowledgeBaseID != "available" {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

// TestSearchReadsCursorContext 验证游标模式直接读取指定知识库中的周边分段。
func TestSearchReadsCursorContext(t *testing.T) {
	sources := []Source{{ID: "base-a", Name: "知识库 A", Read: func(_ context.Context, cursor Cursor, before, after int) ([]Record, error) {
		if cursor.SegmentID != "segment-2" || cursor.SegmentBatchID != "batch-1" || before != 1 || after != 2 {
			t.Fatalf("cursor = %#v, before = %d, after = %d", cursor, before, after)
		}
		return []Record{{DocumentID: "document-1", SegmentID: "segment-1", SegmentBatchID: "batch-1", Position: 1}, {DocumentID: "document-1", SegmentID: "segment-2", SegmentBatchID: "batch-1", Position: 2, Matched: true}}, nil
	}}}
	result, err := Search(context.Background(), sources, Request{
		Cursor: &Cursor{KnowledgeBaseID: "base-a", DocumentID: "document-1", SegmentID: "segment-2", SegmentBatchID: "batch-1", Position: 2},
		Before: 1, After: 2,
	})
	if err != nil || len(result.Records) != 2 || !result.Records[1].Matched {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	// 阅读结果携带知识库标识和自身游标。
	first := result.Records[0]
	if first.KnowledgeBaseID != "base-a" || first.KnowledgeBaseName != "知识库 A" ||
		first.Cursor != (Cursor{KnowledgeBaseID: "base-a", DocumentID: "document-1", SegmentID: "segment-1", SegmentBatchID: "batch-1", Position: 1}) {
		t.Fatalf("first = %#v", first)
	}
	// 缺少批次编号的游标不可读取。
	if _, err := Search(context.Background(), sources, Request{Cursor: &Cursor{KnowledgeBaseID: "base-a", DocumentID: "document-1", SegmentID: "segment-2", Position: 2}}); err == nil {
		t.Fatal("cursor without segment batch should be rejected")
	}
}

// TestSearchRejectsQueriesWithCursor 验证关键词与游标的互斥约束。
func TestSearchRejectsQueriesWithCursor(t *testing.T) {
	cursor := &Cursor{KnowledgeBaseID: " base-a ", DocumentID: " document-1 ", SegmentID: " segment-2 ", Position: 2}
	_, err := Search(context.Background(), nil, Request{Queries: []string{"查询"}, Cursor: cursor})
	if err == nil {
		t.Fatal("queries and cursor should be rejected")
	}
	if cursor.KnowledgeBaseID != " base-a " || cursor.DocumentID != " document-1 " || cursor.SegmentID != " segment-2 " {
		t.Fatalf("cursor was modified: %#v", cursor)
	}
}
