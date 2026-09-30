package searchtext

import (
	"strings"
	"testing"
)

// TestWordVector 验证分段词元同时包含单字、编号片段和分词词元且位置对齐。
func TestWordVector(t *testing.T) {
	vector := WordVector("退款流程说明 SKU-2048A")
	for _, expected := range []string{"'退':1", "'款':2", "'退款':1", "'流程':3", "'sku':7", "'2048':8", "'a':9"} {
		if !strings.Contains(vector, expected) {
			t.Fatalf("vector=%s missing %s", vector, expected)
		}
	}
	if strings.Contains(vector, "~") {
		t.Fatalf("vector=%s contains pinyin", vector)
	}
}

// TestWordQuery 验证自然语言问句按词任一匹配、停用词被丢弃、编号按相邻短语匹配。
func TestWordQuery(t *testing.T) {
	query, ok := WordQuery("如何申请退款？")
	if !ok || !strings.Contains(query, "'申请'") || !strings.Contains(query, "'退款'") || strings.Contains(query, "'如何'") || strings.Contains(query, "&") {
		t.Fatalf("query=%q ok=%v", query, ok)
	}
	query, ok = WordQuery("税")
	if !ok || query != "'税'" {
		t.Fatalf("query=%q ok=%v", query, ok)
	}
	query, ok = WordQuery("E-731 的说明")
	if !ok || !strings.Contains(query, "('e' <-> '731')") || !strings.Contains(query, "'说明'") || strings.Contains(query, "'的'") {
		t.Fatalf("query=%q ok=%v", query, ok)
	}
	if query, ok := WordQuery("什么？"); ok {
		t.Fatalf("query=%q", query)
	}
	if query, ok := WordQuery("  ，。 "); ok {
		t.Fatalf("query=%q", query)
	}
}
