package connectiontest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// maxLoggedBodyBytes 是外部 HTTP 请求与响应日志中请求体和响应体的字节上限。
	maxLoggedBodyBytes = 2 << 10
	// maxResponseBytes 是外部 HTTP 响应允许读取的最大字节数。
	maxResponseBytes = 16 << 20
)

// HTTPDoer 定义外部 HTTP 请求需要的最小客户端契约。
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// NewHTTPClient 创建不跟随重定向的 HTTP 客户端。
func NewHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ReadHTTPResponse 读取外部 HTTP 响应，并规范化传输、状态和响应契约错误。
func ReadHTTPResponse(ctx context.Context, client HTTPDoer, request *http.Request, decode func(io.Reader) error) error {
	body := ""
	if request.Body != nil {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return NewError(StageConnect, FailureProtocol, err)
		}
		body = string(data)
		request.Body = io.NopCloser(bytes.NewReader(data))
	}
	requestAttributes := []any{"method", request.Method, "url", request.URL.String()}
	if request.Method != http.MethodGet {
		requestAttributes = append(requestAttributes, "body", loggedBody([]byte(body)))
	}
	slog.Info("外部 HTTP 请求", requestAttributes...)
	startedAt := time.Now()
	response, err := client.Do(request.Clone(ctx))
	if err != nil {
		return ClassifyTransportError(StageConnect, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return NewError(StageCapability, FailureProtocol, err)
	}
	if len(responseBody) > maxResponseBytes {
		return NewError(StageCapability, FailureProtocol, errors.New("response too large"))
	}
	responseAttributes := []any{
		"method", request.Method,
		"url", request.URL.String(),
		"status_code", response.StatusCode,
		"duration_ms", time.Since(startedAt).Milliseconds(),
	}
	if request.Method != http.MethodGet {
		responseAttributes = append(responseAttributes, "body", loggedBody(responseBody))
	}
	slog.Info("外部 HTTP 响应", responseAttributes...)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return HTTPStatusError(response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if err := decode(bytes.NewReader(responseBody)); err != nil {
		// 保留已分类的探测错误。
		if _, ok := errors.AsType[*Error](err); ok {
			return err
		}
		return NewError(StageCapability, FailureProtocol, err)
	}
	return nil
}

// loggedBody 把 JSON 中的 Unicode 转义转换为可直接阅读的字符，超出日志上限时截断并以省略号结尾。
func loggedBody(data []byte) string {
	text := string(data)
	var value any
	if err := json.Unmarshal(data, &value); err == nil {
		if encoded, err := json.Marshal(value); err == nil {
			text = string(encoded)
		}
	}
	if len(text) <= maxLoggedBodyBytes {
		return text
	}
	return strings.ToValidUTF8(text[:maxLoggedBodyBytes], "") + "…"
}

// AppendPath 在保留自定义基础路径的前提下追加接口路径。
func AppendPath(baseURL, path string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	escapedPath := strings.TrimLeft(path, "/")
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", err
	}
	parsed.RawPath = strings.TrimSuffix(parsed.EscapedPath(), "/") + "/" + escapedPath
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/" + decodedPath
	return parsed.String(), nil
}

// InvalidConfigError 创建连接配置错误。
func InvalidConfigError(err error) error {
	return NewError(StageConnect, FailureInvalidConfig, err)
}

// ValidateDataList 校验带 data 数组的列表响应最小契约。
func ValidateDataList(reader io.Reader) error {
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return err
	}
	if len(payload.Data) == 0 || payload.Data[0] != '[' {
		return errors.New("list response does not contain a data array")
	}
	return nil
}
