package openapi

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/support/arr"
)

const (
	// maxSpecBytes 是读取 OpenAPI 文档的最大字节数。
	maxSpecBytes = 16 << 20
	// maxResponseBytes 是接口响应正文的最大字节数。
	maxResponseBytes = 8 << 20
)

// Source 定义 OpenAPI 文档来源：SpecURL 非空时从该地址读取，否则使用 Spec 正文。
type Source struct {
	SpecURL string
	Spec    string
}

// Discoverer 读取 OpenAPI 文档并转换为工具目录。
type Discoverer interface {
	Discover(context.Context, Source) (Document, error)
}

// Client 读取 OpenAPI 文档并按工具对应的接口发出请求。
type Client struct {
	runner *connectiontest.Runner
	http   *http.Client
}

// NewClient 创建读取文档具有统一超时的 OpenAPI 客户端。
func NewClient() *Client {
	return &Client{runner: connectiontest.NewRunner(10 * time.Second), http: connectiontest.NewHTTPClient()}
}

// Discover 读取文档并转换为工具目录；读取失败按连接失败分类，文档无效时返回 ErrSpecInvalid 或 ErrSpecEmpty。
func (c *Client) Discover(ctx context.Context, source Source) (Document, error) {
	content := []byte(source.Spec)
	if source.SpecURL != "" {
		err := c.runner.Run(ctx, connectiontest.Target{
			Category: string(domain.ConnectionProbeOpenAPI), Adapter: string(domain.BusinessSystemTransportHTTP), Location: string(domain.ConnectionProbeServer),
		}, connectiontest.ProbeFunc(func(ctx context.Context) error {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.SpecURL, nil)
			if err != nil {
				return connectiontest.InvalidConfigError(err)
			}
			request.Header.Set("Accept", "application/json, application/yaml;q=0.9, */*;q=0.8")
			response, err := c.http.Do(request)
			if err != nil {
				return connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
			}
			defer response.Body.Close()
			if response.StatusCode >= 300 {
				return connectiontest.HTTPStatusError(response.StatusCode)
			}
			content, err = io.ReadAll(io.LimitReader(response.Body, maxSpecBytes+1))
			if err != nil {
				return connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
			}
			return nil
		}))
		if err != nil {
			return Document{}, err
		}
	}
	if len(content) > maxSpecBytes {
		return Document{}, fmt.Errorf("%w: document exceeds %d bytes", ErrSpecInvalid, maxSpecBytes)
	}
	return Parse(content, source.SpecURL)
}

// Request 定义一次接口调用：接口根地址、附加到请求的请求头、工具对应的接口与工具输入。
type Request struct {
	BaseURL   string
	Headers   map[string]string
	Operation domain.HTTPOperation
	Arguments json.RawMessage
}

// Call 按工具输入组装请求并返回响应正文：正文超过 maxResponseBytes 时返回错误，2xx 响应的正文原样返回，正文为空时返回状态码；其他状态码作为错误返回，错误含状态码与响应正文。
func (c *Client) Call(ctx context.Context, input Request) (string, error) {
	request, err := buildRequest(ctx, input)
	if err != nil {
		return "", err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("call %s %s: %w", input.Operation.Method, input.Operation.Path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read response of %s %s: %w", input.Operation.Method, input.Operation.Path, err)
	}
	if len(body) > maxResponseBytes {
		return "", fmt.Errorf("响应超过 %d MB，请缩小查询范围后重试。", maxResponseBytes>>20)
	}
	text := strings.TrimSpace(string(body))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", response.StatusCode, text)
	}
	if text == "" {
		return fmt.Sprintf("HTTP %d", response.StatusCode), nil
	}
	return text, nil
}

// buildRequest 把工具输入按参数位置写入路径、查询串、请求头与 JSON 请求体，缺少路径参数时返回错误。
func buildRequest(ctx context.Context, input Request) (*http.Request, error) {
	arguments := map[string]json.RawMessage{}
	if len(input.Arguments) > 0 && string(input.Arguments) != "null" {
		if err := json.Unmarshal(input.Arguments, &arguments); err != nil {
			return nil, errors.New("参数不是 JSON 对象，请重新提交。")
		}
	}
	path := input.Operation.Path
	query := url.Values{}
	headers := http.Header{}
	var body json.RawMessage
	var fields map[string]json.RawMessage
	for _, parameter := range input.Operation.Parameters {
		value, ok := arguments[parameter.Name]
		// 请求体保留显式给出的 null，其他位置的 null 视为未提供。
		if !ok || string(value) == "null" && parameter.In != domain.HTTPParameterInBody {
			if parameter.In == domain.HTTPParameterInPath {
				return nil, fmt.Errorf("缺少路径参数 %s，请重新提交。", parameter.Name)
			}
			continue
		}
		switch parameter.In {
		case domain.HTTPParameterInPath:
			path = strings.ReplaceAll(path, "{"+parameter.Key+"}", url.PathEscape(simple(value)))
		case domain.HTTPParameterInQuery:
			addQuery(query, parameter, value)
		case domain.HTTPParameterInHeader:
			headers.Set(parameter.Key, simple(value))
		case domain.HTTPParameterInBody:
			if parameter.Key == "" {
				body = value
			} else {
				if fields == nil {
					fields = map[string]json.RawMessage{}
				}
				fields[parameter.Key] = value
			}
		}
	}
	if fields != nil {
		encoded, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		body = encoded
	}
	address := strings.TrimRight(input.BaseURL, "/") + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, input.Operation.Method, address, reader)
	if err != nil {
		return nil, err
	}
	request.Header = headers
	request.Header.Set("Accept", "application/json, */*;q=0.8")
	// 请求体按文档声明的媒体类型提交。
	if body != nil {
		request.Header.Set("Content-Type", cmp.Or(input.Operation.ContentType, "application/json"))
	}
	for name, value := range input.Headers {
		request.Header.Set(name, value)
	}
	return request, nil
}

// addQuery 按参数的 style 与 explode 把取值写入查询串：数组展开时逐项写入同名键，否则按 form、spaceDelimited、pipeDelimited 以逗号、空格、竖线连接；
// 对象按 deepObject 写成「键[属性]」，按 form 展开时逐个属性写入，否则以逗号连接属性名与属性值；其他取值写入一项。
func addQuery(query url.Values, parameter domain.HTTPParameter, value json.RawMessage) {
	var items []json.RawMessage
	if json.Unmarshal(value, &items) == nil {
		if parameter.Explode {
			for _, item := range items {
				query.Add(parameter.Key, scalar(item))
			}
			return
		}
		separator := map[string]string{"spaceDelimited": " ", "pipeDelimited": "|"}[parameter.Style]
		query.Add(parameter.Key, joinScalars(items, cmp.Or(separator, ",")))
		return
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(value, &object) == nil {
		names := slices.Sorted(maps.Keys(object))
		switch {
		case parameter.Style == "deepObject":
			for _, name := range names {
				query.Add(parameter.Key+"["+name+"]", scalar(object[name]))
			}
		case parameter.Explode:
			for _, name := range names {
				query.Add(name, scalar(object[name]))
			}
		default:
			pairs := make([]string, 0, 2*len(names))
			for _, name := range names {
				pairs = append(pairs, name, scalar(object[name]))
			}
			query.Add(parameter.Key, strings.Join(pairs, ","))
		}
		return
	}
	query.Add(parameter.Key, scalar(value))
}

// simple 按 OpenAPI 的 simple 方式把路径与请求头参数转为文本：数组以逗号连接各项，其他取值同 scalar。
func simple(value json.RawMessage) string {
	var items []json.RawMessage
	if json.Unmarshal(value, &items) == nil {
		return joinScalars(items, ",")
	}
	return scalar(value)
}

// joinScalars 以分隔符连接各项的文本。
func joinScalars(items []json.RawMessage, separator string) string {
	return strings.Join(arr.Map(items, scalar), separator)
}

// scalar 把参数值转为请求中的文本：字符串取其内容，其他值取 JSON 文本。
func scalar(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	return string(value)
}
