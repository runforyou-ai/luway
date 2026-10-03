//go:build !server

package apiproxy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// DeviceModelEndpoint 返回指定运行在企业服务器上的模型网关入口，以及为模型请求附加登录令牌与本机设备编号的传输层；meta 必须携带设备编号。
func (b *Backend) DeviceModelEndpoint(ctx context.Context, meta appservice.RequestMeta, runID string) (string, http.RoundTripper, error) {
	state := b.connection.currentState()
	if state == nil {
		return "", nil, appservice.SessionError(meta, appservice.SessionStateConnect, i18n.ErrorServerConnectionRequired)
	}
	credential, authenticated := b.sessions.Current(ctx, state.baseURL.String())
	if !authenticated {
		return "", nil, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	base := state.client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	endpoint := remoteEndpoint(state.baseURL, "/agent-runs/"+url.PathEscape(runID)+"/model", "")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", nil, appservice.FailedError(meta, i18n.ErrorRemoteRequestCreateFailed, err)
	}
	return endpoint, &deviceModelTransport{base: base, endpoint: parsed, token: credential.Token, deviceID: meta.DeviceID}, nil
}

// deviceModelTransport 只向模型网关入口发出请求，并为请求写入登录令牌、本机设备编号与接口版本。
type deviceModelTransport struct {
	base     http.RoundTripper
	endpoint *url.URL
	token    string
	deviceID string
}

// RoundTrip 拒绝模型网关入口之外的地址，复制请求并写入认证与接口版本请求头后发出。
func (t *deviceModelTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != t.endpoint.Scheme || request.URL.Host != t.endpoint.Host || request.URL.Path != t.endpoint.Path {
		return nil, fmt.Errorf("device model request outside gateway endpoint: %s://%s%s", request.URL.Scheme, request.URL.Host, request.URL.Path)
	}
	outgoing := request.Clone(request.Context())
	outgoing.Header.Set("Authorization", "Bearer "+t.token)
	outgoing.Header.Set(appservice.DeviceHeader, t.deviceID)
	outgoing.Header.Set(appservice.ClientAPIVersionHeader, strconv.Itoa(appservice.APIVersion))
	return t.base.RoundTrip(outgoing)
}
