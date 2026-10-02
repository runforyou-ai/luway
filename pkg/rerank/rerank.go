//go:build server

// Package rerank 调用重排模型接口为候选文本按与查询的相关性打分。
package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Protocol 标识重排接口的请求格式。
type Protocol string

const (
	// ProtocolCompatible 是 Cohere、Jina 等通用的 rerank 接口格式。
	ProtocolCompatible Protocol = "compatible"
	// ProtocolDashScope 是阿里云 DashScope 原生重排接口格式。
	ProtocolDashScope Protocol = "dashscope"
)

// maxResponseBytes 是重排响应允许读取的最大字节数。
const maxResponseBytes = 4 << 20

// Credential 提供访问重排模型所需的接口格式、接口地址和密钥。
type Credential struct {
	Protocol Protocol
	BaseURL  string
	APIKey   string
}

// Score 表示一条候选文本的相关性得分，Index 为候选在请求中的下标。
type Score struct {
	Index     int
	Relevance float64
}

// Error 定义重排调用的语言无关失败原因码。
type Error struct {
	Code string
}

// Error 返回语言无关的失败原因。
func (e *Error) Error() string { return "rerank: " + e.Code }

// Result 定义一次重排的得分与 Token 用量。
type Result struct {
	Scores      []Score
	InputTokens int // 响应中 usage.total_tokens，接口未返回用量时为 0。
}

// Client 通过重排接口打分。
type Client struct{ http *http.Client }

// NewClient 创建重排客户端。
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: time.Minute}}
}

// Rerank 按接口格式提交查询与候选文本，返回候选下标与相关性得分；接口没有返回任何得分时视为失败。
func (c *Client) Rerank(ctx context.Context, credential Credential, model, query string, documents []string, topN int) (Result, error) {
	endpoint, err := Endpoint(credential.Protocol, credential.BaseURL)
	if err != nil {
		return Result{}, &Error{Code: "rerank_model_unavailable"}
	}
	// 按接口格式组织请求体。
	var payload any
	if credential.Protocol == ProtocolDashScope {
		payload = map[string]any{
			"model":      model,
			"input":      map[string]any{"query": query, "documents": documents},
			"parameters": map[string]any{"return_documents": false, "top_n": topN},
		}
	} else {
		payload = map[string]any{"model": model, "query": query, "documents": documents, "top_n": topN}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, &Error{Code: "rerank_model_unavailable"}
	}
	request.Header.Set("Content-Type", "application/json")
	// 无凭据的自建或本机服务不携带鉴权头。
	if apiKey := strings.TrimSpace(credential.APIKey); apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return Result{}, &Error{Code: "rerank_timeout"}
		}
		return Result{}, &Error{Code: "rerank_model_unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return Result{}, &Error{Code: "rerank_model_unavailable"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, &Error{Code: "rerank_failed"}
	}
	var decoded struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
		Output struct {
			Results []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
			} `json:"results"`
		} `json:"output"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&decoded); err != nil {
		return Result{}, &Error{Code: "rerank_failed"}
	}
	results := decoded.Results
	if credential.Protocol == ProtocolDashScope {
		results = decoded.Output.Results
	}
	if len(results) == 0 {
		return Result{}, &Error{Code: "rerank_failed"}
	}
	scores := make([]Score, 0, len(results))
	for _, item := range results {
		if item.Index < 0 || item.Index >= len(documents) {
			return Result{}, &Error{Code: "rerank_failed"}
		}
		scores = append(scores, Score{Index: item.Index, Relevance: item.RelevanceScore})
	}
	return Result{Scores: scores, InputTokens: decoded.Usage.TotalTokens}, nil
}

// Endpoint 按接口格式改写接口地址的路径后返回重排接口地址。
func Endpoint(protocol Protocol, value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("parse model base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("model base URL must include scheme and host")
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if protocol == ProtocolDashScope {
		for _, suffix := range []string{"/compatible-mode/v1", "/api/v1", "/v1"} {
			path = strings.TrimSuffix(path, suffix)
		}
		path += "/api/v1/services/rerank/text-rerank/text-rerank"
	} else {
		path += "/rerank"
	}
	parsed.Path = path
	parsed.RawPath = ""
	return parsed.String(), nil
}
