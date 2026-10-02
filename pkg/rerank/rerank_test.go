//go:build server

package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRerankProtocols 验证 DashScope 与通用两种接口格式的请求体、鉴权和得分解析。
func TestRerankProtocols(t *testing.T) {
	var path string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, dashScope := body["input"]; dashScope {
			_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"results": []map[string]any{{"index": 1, "relevance_score": 0.9}, {"index": 0, "relevance_score": 0.2}}}, "usage": map[string]any{"total_tokens": 12}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"index": 0, "relevance_score": 0.7}}})
	}))
	defer server.Close()
	client := NewClient()

	result, err := client.Rerank(context.Background(), Credential{Protocol: ProtocolDashScope, BaseURL: server.URL + "/compatible-mode/v1", APIKey: "secret"}, "qwen3-rerank", "退款", []string{"甲", "乙"}, 2)
	scores := result.Scores
	if err != nil || result.InputTokens != 12 || path != "/api/v1/services/rerank/text-rerank/text-rerank" || len(scores) != 2 || scores[0].Index != 1 || scores[0].Relevance != 0.9 {
		t.Fatalf("scores=%+v path=%s err=%v", scores, path, err)
	}
	if input, _ := body["input"].(map[string]any); input["query"] != "退款" || body["parameters"].(map[string]any)["top_n"] != float64(2) {
		t.Fatalf("body=%v", body)
	}

	result, err = client.Rerank(context.Background(), Credential{Protocol: ProtocolCompatible, BaseURL: server.URL + "/v1", APIKey: "secret"}, "rerank", "退款", []string{"甲"}, 1)
	scores = result.Scores
	if err != nil || path != "/v1/rerank" || len(scores) != 1 || scores[0].Relevance != 0.7 || body["query"] != "退款" {
		t.Fatalf("scores=%+v path=%s body=%v err=%v", scores, path, body, err)
	}

	_, err = client.Rerank(context.Background(), Credential{Protocol: ProtocolCompatible, BaseURL: server.URL + "/v1", APIKey: "wrong"}, "rerank", "退款", []string{"甲"}, 1)
	if failure, ok := err.(*Error); !ok || failure.Code != "rerank_model_unavailable" {
		t.Fatalf("err=%v", err)
	}
}
