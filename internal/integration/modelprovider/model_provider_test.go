package modelprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// TestRegistryUsesProviderReadOnlyEndpoints 验证内置供应商通过只读模型列表接口测试连接。
func TestRegistryUsesProviderReadOnlyEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		brand      domain.AIProviderBrand
		basePath   string
		wantPath   string
		authHeader string
		response   string
	}{
		{name: "DeepSeek", brand: domain.AIProviderBrandDeepSeek, wantPath: "/models", response: `{"object":"list","data":[]}`},
		{name: "OpenAI", brand: domain.AIProviderBrandOpenAI, basePath: "/v1", wantPath: "/v1/models", response: `{"object":"list","data":[]}`},
		{name: "Anthropic", brand: domain.AIProviderBrandAnthropic, wantPath: "/v1/models", authHeader: "x-api-key", response: `{"data":[],"has_more":false}`},
		{name: "Google", brand: domain.AIProviderBrandGoogle, wantPath: "/v1beta/models", authHeader: "x-goog-api-key", response: `{"models":[]}`},
		{name: "阿里云百炼", brand: domain.AIProviderBrandAlibaba, wantPath: "/api/v1/models", response: `{"success":true,"output":{"models":[]}}`},
		{name: "月之暗面", brand: domain.AIProviderBrandMoonshot, basePath: "/v1", wantPath: "/v1/models", response: `{"object":"list","data":[]}`},
		{name: "智谱", brand: domain.AIProviderBrandZhipu, basePath: "/api/paas/v4", wantPath: "/api/paas/v4/models", response: `{"object":"list","data":[]}`},
		{name: "火山引擎", brand: domain.AIProviderBrandVolcengine, basePath: "/api/v3", wantPath: "/api/v3/models", response: `{"object":"list","data":[]}`},
		{name: "MiniMax", brand: domain.AIProviderBrandMiniMax, basePath: "/v1", wantPath: "/v1/models", response: `{"object":"list","data":[]}`},
		{name: "xAI", brand: domain.AIProviderBrandXAI, basePath: "/v1", wantPath: "/v1/models", response: `{"object":"list","data":[]}`},
		{name: "Mistral", brand: domain.AIProviderBrandMistral, basePath: "/v1", wantPath: "/v1/models", response: `{"object":"list","data":[]}`},
		{name: "OpenRouter", brand: domain.AIProviderBrandOpenRouter, basePath: "/api/v1", wantPath: "/api/v1/key", response: `{"data":{"label":"sk-or"}}`},
		{name: "TypeSafe", brand: domain.AIProviderBrandTypeSafe, basePath: "/v1", wantPath: "/v1/models", response: `{"models":[{"name":"jev-latest"}]}`},
		{name: "Ollama", brand: domain.AIProviderBrandOllama, wantPath: "/api/tags", response: `{"models":[]}`},
		{name: "OpenAI 兼容", brand: domain.AIProviderBrandOpenAICompatible, basePath: "/v1", wantPath: "/v1/models", response: `{"object":"list","data":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != test.wantPath {
					t.Errorf("request = %s %s, want GET %s", request.Method, request.URL.Path, test.wantPath)
				}
				// 没有指定专用凭据头的品牌统一使用 Bearer 认证。
				if test.authHeader != "" && request.Header.Get(test.authHeader) != "test-key" {
					t.Errorf("missing %s credential", test.authHeader)
				}
				if test.authHeader == "" && request.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("missing bearer authorization")
				}
				if test.brand == domain.AIProviderBrandAnthropic && request.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("missing anthropic-version header")
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.response))
			}))

			probe, err := NewRegistry(server.Client()).NewProbe(Config{
				Brand: test.brand, APIKey: "test-key", APIURL: server.URL + test.basePath,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := probe.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestProbeClassifiesAuthenticationFailure 验证供应商拒绝密钥时返回统一认证错误。
func TestProbeClassifiesAuthenticationFailure(t *testing.T) {
	server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	probe, err := NewRegistry(server.Client()).NewProbe(Config{
		Brand: domain.AIProviderBrandDeepSeek, APIKey: "invalid-key", APIURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = probe.Run(context.Background())
	stage, kind, ok := connectiontest.Details(err)
	if !ok || stage != connectiontest.StageAuthenticate || kind != connectiontest.FailureUnauthorized {
		t.Fatalf("stage = %q, kind = %q, ok = %v", stage, kind, ok)
	}
}

// TestAlibabaModelsURLAcceptsCompatibleBaseURL 验证百炼兼容模式地址会切换到原生模型列表路径。
func TestAlibabaModelsURLAcceptsCompatibleBaseURL(t *testing.T) {
	got, err := alibabaModelsURL("https://dashscope.aliyuncs.com/compatible-mode/v1")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://dashscope.aliyuncs.com/api/v1/models"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

// TestProbeOmitsAuthorizationWithoutCredential 验证无凭据的自建或本机服务探测时不携带鉴权头。
func TestProbeOmitsAuthorizationWithoutCredential(t *testing.T) {
	for _, test := range []struct {
		brand    domain.AIProviderBrand
		basePath string
		wantPath string
		response string
	}{
		{brand: domain.AIProviderBrandOllama, wantPath: "/api/tags", response: `{"models":[]}`},
		{brand: domain.AIProviderBrandOpenAICompatible, basePath: "/v1", wantPath: "/v1/models", response: `{"object":"list","data":[]}`},
	} {
		t.Run(string(test.brand), func(t *testing.T) {
			server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.wantPath {
					t.Errorf("path = %s, want %s", request.URL.Path, test.wantPath)
				}
				if authorization := request.Header.Get("Authorization"); authorization != "" {
					t.Errorf("authorization = %q, want empty", authorization)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.response))
			}))

			probe, err := NewRegistry(server.Client()).NewProbe(Config{
				Brand: test.brand, APIURL: server.URL + test.basePath,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := probe.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
