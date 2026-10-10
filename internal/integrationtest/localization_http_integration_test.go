//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/productsite"
	"github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"
)

// TestCoreLocalizedHTTP 验证独立核心后端在已安装平台上输出中英文登录错误，并正常渲染公开首页模板。
func TestCoreLocalizedHTTP(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	servertest.InstallWorkspace(t, store.DB(), servertest.WorkspaceSpec{
		Name: "本地化", DisplayName: "管理员", Email: servertest.UniqueEmail("localized-http"), Password: "password123",
	})
	backend := newAccountTestBackend(t, store.DB())
	handler := api.NewService(backend)
	site := productsite.NewService("/site.css", &clientrelease.Catalog{}, func(ctx context.Context) (productsite.Home, error) {
		_, err := backend.InstallationStatus(ctx, appservice.RequestMeta{})
		return productsite.Home{}, err
	})
	for _, item := range []struct{ locale, path, message string }{
		{"zh-CN", "/zh-cn/", "请先登录。"},
		{"en-US", "/en/", "Please log in first."},
	} {
		request := httptest.NewRequest(http.MethodGet, "/platform/settings", nil)
		request.Header.Set("Accept-Language", item.locale)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		var failure struct {
			Error appservice.Error `json:"error"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
		require.Equal(t, item.message, failure.Error.Message)
		require.Equal(t, item.locale, response.Header().Get("Content-Language"))
		response = httptest.NewRecorder()
		site.ServeHTTP(response, httptest.NewRequest(http.MethodGet, item.path, nil))
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `href="/app/"`)
		require.NotContains(t, response.Body.String(), `id="pricing"`)
	}
}
