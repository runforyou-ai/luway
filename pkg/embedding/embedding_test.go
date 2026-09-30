//go:build server

package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// embeddingServer 按请求顺序返回乱序但带 index 的向量，并记录每批输入条数。
func embeddingServer(t *testing.T, dimension int, batches *[]int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("request=%s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var input struct {
			Model      string   `json:"model"`
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Model != "embedding-test" || input.Dimensions != dimension {
			t.Errorf("input=%+v", input)
		}
		*batches = append(*batches, len(input.Input))
		data := make([]map[string]any, 0, len(input.Input))
		// 倒序返回，验证调用方按 index 还原顺序。
		for index := len(input.Input) - 1; index >= 0; index-- {
			vector := make([]float32, dimension)
			vector[0] = float32(index)
			data = append(data, map[string]any{"index": index, "embedding": vector})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

// TestEmbedBatchesAndOrder 验证按固定批量拆分并按 index 还原输入顺序。
func TestEmbedBatchesAndOrder(t *testing.T) {
	var batches []int
	server := embeddingServer(t, 8, &batches)
	defer server.Close()
	inputs := make([]string, 45)
	for index := range inputs {
		inputs[index] = fmt.Sprintf("第%d段", index)
	}
	vectors, err := NewClient().Embed(context.Background(), Credential{BaseURL: server.URL, APIKey: "test-key"}, "embedding-test", 8, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 3 || batches[0] != 20 || batches[1] != 20 || batches[2] != 5 {
		t.Fatalf("batches=%v", batches)
	}
	if len(vectors) != 45 {
		t.Fatalf("vectors=%d", len(vectors))
	}
	for index, vector := range vectors {
		if len(vector) != 8 || int(vector[0]) != index%BatchSize {
			t.Fatalf("vector[%d]=%v", index, vector)
		}
	}
}

// TestEmbedFailures 验证地址缺失、维度不符和调用失败分别返回对应原因码。
func TestEmbedFailures(t *testing.T) {
	client := NewClient()
	for _, credential := range []Credential{{}, {APIKey: "test-key"}} {
		_, err := client.Embed(context.Background(), credential, "embedding-test", 8, []string{"正文"})
		var failure *Error
		if !errors.As(err, &failure) || failure.Code != "embedding_model_unavailable" {
			t.Fatalf("credential=%+v error=%v", credential, err)
		}
	}

	mismatched := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2]}]}`))
	}))
	defer mismatched.Close()
	_, err := client.Embed(context.Background(), Credential{BaseURL: mismatched.URL, APIKey: "test-key"}, "embedding-test", 8, []string{"正文"})
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "embedding_dimension_mismatch" {
		t.Fatalf("error=%v", err)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte("secret provider detail"))
	}))
	defer failing.Close()
	_, err = client.Embed(context.Background(), Credential{BaseURL: failing.URL, APIKey: "test-key"}, "embedding-test", 8, []string{"正文"})
	if !errors.As(err, &failure) || failure.Code != "embedding_failed" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error=%v", err)
	}

	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer short.Close()
	_, err = client.Embed(context.Background(), Credential{BaseURL: short.URL, APIKey: "test-key"}, "embedding-test", 8, []string{"正文"})
	if !errors.As(err, &failure) || failure.Code != "embedding_failed" {
		t.Fatalf("error=%v", err)
	}
}

// TestEmbedWithoutCredential 验证无凭据的自建或本机服务不携带鉴权头且正常返回向量。
func TestEmbedWithoutCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Errorf("authorization = %q, want empty", authorization)
		}
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2]}]}`))
	}))
	defer server.Close()

	vectors, err := NewClient().Embed(context.Background(), Credential{BaseURL: server.URL}, "embedding-test", 2, []string{"正文"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 2 {
		t.Fatalf("vectors = %v", vectors)
	}
}
