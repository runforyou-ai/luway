package modelgateway

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
)

// maxErrorBodyBytes 是网关拒绝请求时读取的响应体字节上限。
const maxErrorBodyBytes = 4 << 10

// Models 返回经服务端模型网关调用的对话模型组件工厂，transport 负责附加设备认证。
func Models(endpoint string, transport http.RoundTripper) agentruntime.ModelFactory {
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return func(_ context.Context, options agentruntime.ModelOptions) (model.AgenticModel, error) {
		return &gatewayModel{client: client, endpoint: endpoint, options: options}, nil
	}
}

// gatewayModel 把模型请求编码后发给服务端模型网关，并解码网关返回的输出帧。
type gatewayModel struct {
	client   *http.Client
	endpoint string
	options  agentruntime.ModelOptions
}

// Generate 请求完整输出。
func (m *gatewayModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	body, err := m.post(ctx, input, opts, false)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var frame Frame
	if err := gob.NewDecoder(body).Decode(&frame); err != nil {
		return nil, fmt.Errorf("decode model gateway response: %w", err)
	}
	if frame.Error != "" {
		return nil, errors.New(frame.Error)
	}
	return frame.Message, nil
}

// Stream 请求流式输出，调用方读取时解码下一帧，网关以错误帧结束时返回该错误；调用方关闭读取端时立即关闭响应体。
func (m *gatewayModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	body, err := m.post(ctx, input, opts, true)
	if err != nil {
		return nil, err
	}
	decoder := gob.NewDecoder(body)
	done := false
	return modelprovider.PullStream(func() (*schema.AgenticMessage, error) {
		if done {
			return nil, io.EOF
		}
		var frame Frame
		if err := decoder.Decode(&frame); errors.Is(err, io.EOF) {
			done = true
			return nil, io.EOF
		} else if err != nil {
			done = true
			return nil, fmt.Errorf("decode model gateway stream: %w", err)
		}
		if frame.Error != "" {
			done = true
			return nil, errors.New(frame.Error)
		}
		return frame.Message, nil
	}, func() { body.Close() }), nil
}

// post 编码请求并发给网关，网关拒绝请求时返回带状态码与响应摘要的错误。
func (m *gatewayModel) post(ctx context.Context, input []*schema.AgenticMessage, opts []model.Option, stream bool) (io.ReadCloser, error) {
	options, err := EncodeOptions(opts)
	if err != nil {
		return nil, err
	}
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(Request{Model: m.options, Stream: stream, Messages: input, Options: options}); err != nil {
		return nil, fmt.Errorf("encode model gateway request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, &payload)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", ContentType)
	response, err := m.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
		response.Body.Close()
		return nil, fmt.Errorf("model gateway rejected request: status %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return response.Body, nil
}
