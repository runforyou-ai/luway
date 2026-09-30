//go:build server

// Package embedding 调用 OpenAI 兼容接口批量生成文本向量。
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	// BatchSize 是每次向量请求的输入条数，取供应商兼容接口允许的最小批量。
	BatchSize = 20
	// maxErrorBodyBytes 是请求失败时记入日志的响应体字节上限。
	maxErrorBodyBytes = 2 << 10
)

// Credential 提供访问向量模型所需的兼容入口和密钥。
type Credential struct {
	BaseURL string
	APIKey  string
}

// Error 定义向量生成的语言无关失败原因码。
type Error struct {
	Code string
}

// Error 返回语言无关的失败原因。
func (e *Error) Error() string { return "embedding: " + e.Code }

// Client 通过 OpenAI 兼容接口生成向量。
type Client struct{ http *http.Client }

// NewClient 创建向量生成客户端。
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: 5 * time.Minute}}
}

// Embed 按固定批量生成输入文本的向量，并校验返回维度。
func (c *Client) Embed(ctx context.Context, credential Credential, model string, dimension int, inputs []string) ([][]float32, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(credential.BaseURL), "/")
	if baseURL == "" {
		return nil, &Error{Code: "embedding_model_unavailable"}
	}
	vectors := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += BatchSize {
		batch := inputs[start:min(start+BatchSize, len(inputs))]
		body, err := json.Marshal(map[string]any{"model": model, "input": batch, "dimensions": dimension})
		if err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, &Error{Code: "embedding_model_unavailable"}
		}
		request.Header.Set("Content-Type", "application/json")
		// 无凭据的自建或本机服务不携带鉴权头。
		if credential.APIKey != "" {
			request.Header.Set("Authorization", "Bearer "+credential.APIKey)
		}
		response, err := c.http.Do(request)
		if err != nil {
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				return nil, &Error{Code: "embedding_failed"}
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &Error{Code: "embedding_failed"}
		}
		// 非 200 响应记录状态码与截断后的响应体。
		if response.StatusCode != http.StatusOK {
			detail, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
			response.Body.Close()
			slog.Warn("向量生成请求失败", "model", model, "status_code", response.StatusCode, "body", strings.ToValidUTF8(string(detail), "�"))
			return nil, &Error{Code: "embedding_failed"}
		}
		var output struct {
			Data []struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&output)
		response.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode embedding response: %w", decodeErr)
		}
		if len(output.Data) != len(batch) {
			return nil, &Error{Code: "embedding_failed"}
		}
		// 供应商按 index 标识输入顺序，取值前按该字段还原。
		ordered := make([][]float32, len(batch))
		for _, item := range output.Data {
			if item.Index < 0 || item.Index >= len(batch) || ordered[item.Index] != nil {
				return nil, &Error{Code: "embedding_failed"}
			}
			if len(item.Embedding) != dimension {
				return nil, &Error{Code: "embedding_dimension_mismatch"}
			}
			ordered[item.Index] = item.Embedding
		}
		vectors = append(vectors, ordered...)
	}
	return vectors, nil
}
