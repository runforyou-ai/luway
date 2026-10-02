//go:build server

package productdocs

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// TestSiteMiddleware 验证文档路径去掉前缀后提供站点文件，目录路径返回 index.html，未命中路径返回站点 404 页面，站点图标被替换，其他请求交给下一个处理器。
func TestSiteMiddleware(t *testing.T) {
	middleware, err := siteMiddleware(fstest.MapFS{
		"index.html":             {Data: []byte("<!doctype html><title>root</title>")},
		"zh-cn/guide/index.html": {Data: []byte("<!doctype html><title>guide</title>")},
		"_astro/page.abc.js":     {Data: []byte("export {}")},
		"favicon.png":            {Data: []byte("build icon")},
		"404.html":               {Data: []byte("<!doctype html><title>404</title>")},
	}, []byte("runtime icon"))
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	})
	handler := middleware(next)
	serve := func(target string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		return response
	}

	if response := serve("/docs"); response.Code != http.StatusMovedPermanently || response.Header().Get("Location") != "/docs/" {
		t.Fatalf("/docs: status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	for target, want := range map[string]string{
		"/docs/":             "<!doctype html><title>root</title>",
		"/docs/zh-cn/guide/": "<!doctype html><title>guide</title>",
		"/docs/zh-cn/guide":  "<!doctype html><title>guide</title>",
		"/docs/favicon.png":  "runtime icon",
	} {
		response := serve(target)
		if response.Code != http.StatusOK || response.Body.String() != want {
			t.Fatalf("%s: status=%d body=%q", target, response.Code, response.Body.String())
		}
	}
	if response := serve("/docs/missing/"); response.Code != http.StatusNotFound || response.Body.String() != "<!doctype html><title>404</title>" {
		t.Fatalf("missing page: status=%d body=%q", response.Code, response.Body.String())
	}
	if response := serve("/assets/app.js"); response.Code != http.StatusTeapot {
		t.Fatalf("non-docs request status = %d", response.Code)
	}

	empty, err := siteMiddleware(fstest.MapFS{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	empty(next).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/docs/", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("site without docs status = %d", response.Code)
	}
}
