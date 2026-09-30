//go:build server

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
)

// deviceModelMaxRequestBytes 是设备单次模型请求体的上限，覆盖随消息直传的附件。
const deviceModelMaxRequestBytes = 64 << 20

// deviceModelProxyOrigin 是计算品牌入口路径前缀时使用的占位源地址。
const deviceModelProxyOrigin = "http://device-model-proxy"

// DeviceModelAuthorizer 校验设备模型代理请求并返回运行锁定的上游模型服务。
type DeviceModelAuthorizer interface {
	// AuthorizeDeviceModelRequest 校验请求来自持有该运行有效租约的本人未撤销设备，并返回上游模型服务。
	AuthorizeDeviceModelRequest(ctx context.Context, meta appservice.RequestMeta, runID string) (direct.DeviceModelUpstream, error)
}

// WithDeviceModelProxy 注入设备模型代理的请求授权。
func WithDeviceModelProxy(authorizer DeviceModelAuthorizer) ServiceOption {
	return func(service *Service) {
		service.deviceModels = authorizer
	}
}

// proxyDeviceModel 把设备运行的模型请求转发给运行锁定的上游模型服务：只放行该品牌的对话接口，要求请求的模型与配置版本一致，换成供应商凭据后逐块透传流式响应。
func (s *Service) proxyDeviceModel(c *gin.Context) {
	meta := requestMeta(c)
	upstream, err := s.deviceModels.AuthorizeDeviceModelRequest(c.Request.Context(), meta, c.Param("runID"))
	if writeApplicationError(c, err) {
		return
	}
	target, err := url.Parse(upstream.BaseURL)
	if err != nil {
		writeApplicationError(c, err)
		return
	}
	// 设备按同一品牌规则拼接入口，去掉品牌附加的路径前缀后接到上游入口。
	proxyBase, err := modelprovider.CompatibleBaseURL(upstream.Brand, deviceModelProxyOrigin)
	if err != nil {
		writeApplicationError(c, err)
		return
	}
	endpoint, ok := strings.CutPrefix(c.Param("path"), strings.TrimPrefix(proxyBase, deviceModelProxyOrigin))
	if !ok || endpoint == "" {
		writeApplicationError(c, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, deviceModelMaxRequestBytes))
	if err != nil {
		writeApplicationError(c, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil))
		return
	}
	if !deviceModelRequestAllowed(upstream, endpoint, body) {
		slog.Warn("设备模型请求不在允许范围内", "agent_run_id", c.Param("runID"), "brand", upstream.Brand, "endpoint", endpoint)
		writeApplicationError(c, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil))
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme, request.Out.URL.Host, request.Out.Host = target.Scheme, target.Host, target.Host
			request.Out.URL.Path = strings.TrimRight(target.Path, "/") + endpoint
			request.Out.URL.RawPath = ""
			query := request.In.URL.Query()
			query.Del("key")
			request.Out.URL.RawQuery = query.Encode()
			request.Out.Body = io.NopCloser(bytes.NewReader(body))
			request.Out.ContentLength = int64(len(body))
			// 去掉设备登录凭据，按品牌写入供应商凭据。
			for _, header := range []string{"Authorization", appservice.DeviceHeader, "X-Api-Key", "X-Goog-Api-Key", "Api-Key", "Cookie"} {
				request.Out.Header.Del(header)
			}
			if upstream.APIKey == "" {
				return
			}
			switch domain.AIProviderBrand(upstream.Brand) {
			case domain.AIProviderBrandAnthropic:
				request.Out.Header.Set("X-Api-Key", upstream.APIKey)
			case domain.AIProviderBrandGoogle:
				request.Out.Header.Set("X-Goog-Api-Key", upstream.APIKey)
			default:
				request.Out.Header.Set("Authorization", "Bearer "+upstream.APIKey)
			}
		},
		FlushInterval: -1,
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, err error) {
			if request.Context().Err() != nil {
				return
			}
			slog.Warn("设备模型请求转发失败", "agent_run_id", c.Param("runID"), "brand", upstream.Brand, "error", err)
			writeApplicationError(c, appservice.UnavailableError(meta, i18n.ErrorDeviceRunRequestFailed, nil))
		},
	}
	// 只向代理暴露写入与刷新能力，连接断开由请求 context 感知。
	proxy.ServeHTTP(struct {
		http.ResponseWriter
		http.Flusher
	}{c.Writer, c.Writer}, c.Request)
}

// deviceModelRequestAllowed 判断请求是否为运行时对该品牌发起的对话接口且模型与配置版本锁定的一致：Google 按完整路径匹配模型资源名，其余品牌按固定接口路径和请求体的 model 字段判断。
func deviceModelRequestAllowed(upstream direct.DeviceModelUpstream, endpoint string, body []byte) bool {
	switch domain.AIProviderBrand(upstream.Brand) {
	case domain.AIProviderBrandGoogle:
		// 模型标识不带资源前缀时按基础模型补全，与 SDK 的拼接规则一致。
		resource := upstream.Identifier
		if !strings.HasPrefix(resource, "models/") && !strings.HasPrefix(resource, "tunedModels/") {
			resource = "models/" + resource
		}
		return endpoint == "/v1beta/"+resource+":generateContent" || endpoint == "/v1beta/"+resource+":streamGenerateContent"
	case domain.AIProviderBrandAnthropic:
		if endpoint != "/v1/messages" {
			return false
		}
	case domain.AIProviderBrandVolcengine:
		if endpoint != "/responses" {
			return false
		}
	default:
		if endpoint != "/chat/completions" {
			return false
		}
	}
	var payload struct {
		Model *string `json:"model"`
	}
	return json.Unmarshal(body, &payload) == nil && payload.Model != nil && *payload.Model == upstream.Identifier
}
