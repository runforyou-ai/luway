//go:build server

package api

import (
	"bytes"
	"context"
	"encoding/gob"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/modelgateway"
)

// stubDeviceModelGateway 按预设返回模型组件工厂或错误，并记录收到的认证信息与创建参数。
type stubDeviceModelGateway struct {
	err     error
	meta    appservice.RequestMeta
	runID   string
	options agentruntime.ModelOptions
}

// DeviceRunModels 记录认证信息并返回回显输入条数的模型组件工厂或预设错误。
func (g *stubDeviceModelGateway) DeviceRunModels(_ context.Context, meta appservice.RequestMeta, runID string) (agentruntime.ModelFactory, error) {
	g.meta, g.runID = meta, runID
	if g.err != nil {
		return nil, g.err
	}
	return func(_ context.Context, options agentruntime.ModelOptions) (model.AgenticModel, error) {
		g.options = options
		return echoModel{}, nil
	}, nil
}

// echoModel 以输入消息条数作为回复正文。
type echoModel struct{}

// Generate 返回输入条数。
func (echoModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{
		schema.NewContentBlock(&schema.AssistantGenText{Text: strings.Repeat("x", len(input))}),
	}}, nil
}

// Stream 以单个分片返回输入条数。
func (m echoModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	message, _ := m.Generate(ctx, input, opts...)
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}

// postDeviceModel 以设备身份向模型网关发出一次请求。
func postDeviceModel(gateway DeviceModelGateway, body []byte) *httptest.ResponseRecorder {
	service := NewService(nil, WithDeviceModelGateway(gateway))
	request := httptest.NewRequest(http.MethodPost, "/agent-runs/run-1/model", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer login-token")
	request.Header.Set(appservice.DeviceHeader, "device-1")
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, request)
	return recorder
}

// TestDeviceModelGateway 验证网关以设备身份取得运行的模型组件，按请求参数执行并逐帧返回输出，认证失败与请求体无效时返回错误响应。
func TestDeviceModelGateway(t *testing.T) {
	var payload bytes.Buffer
	request := modelgateway.Request{
		Model: agentruntime.ModelOptions{MaxOutputTokens: 64, DisableThinking: true}, Stream: true,
		Messages: []*schema.AgenticMessage{schema.UserAgenticMessage("甲"), schema.UserAgenticMessage("乙")},
	}
	if err := gob.NewEncoder(&payload).Encode(request); err != nil {
		t.Fatal(err)
	}
	gateway := &stubDeviceModelGateway{}
	recorder := postDeviceModel(gateway, payload.Bytes())
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != modelgateway.ContentType {
		t.Fatalf("响应 = %d %q", recorder.Code, recorder.Body.String())
	}
	var frame modelgateway.Frame
	if err := gob.NewDecoder(recorder.Body).Decode(&frame); err != nil || frame.Message.ContentBlocks[0].AssistantGenText.Text != "xx" {
		t.Fatalf("frame=%+v err=%v", frame, err)
	}
	if gateway.meta.Token != "login-token" || gateway.meta.DeviceID != "device-1" || gateway.runID != "run-1" ||
		gateway.options.MaxOutputTokens != 64 || !gateway.options.DisableThinking {
		t.Fatalf("网关收到 = %#v %q %#v", gateway.meta, gateway.runID, gateway.options)
	}

	for name, scenario := range map[string]struct {
		gateway *stubDeviceModelGateway
		body    []byte
		status  int
	}{
		"租约失效":  {&stubDeviceModelGateway{err: appservice.ConflictError(appservice.RequestMeta{}, i18n.ErrorDeviceRunLeaseLost, "lease_lost")}, payload.Bytes(), http.StatusConflict},
		"请求体无效": {&stubDeviceModelGateway{}, []byte("not gob"), http.StatusBadRequest},
	} {
		if recorder := postDeviceModel(scenario.gateway, scenario.body); recorder.Code != scenario.status {
			t.Fatalf("%s 响应 = %d %q", name, recorder.Code, recorder.Body.String())
		}
	}
}
