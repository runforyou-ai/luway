package webasset

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestFileServerCachePolicy 验证缓存策略、gzip 协商、固定响应类型、目录索引、路径规范化和错误响应。
func TestFileServerCachePolicy(t *testing.T) {
	script := []byte(strings.Repeat("console.log('app');\n", 200))
	server, err := NewFileServer(fstest.MapFS{
		"index.html":            {Data: []byte("<!doctype html><title>App</title>")},
		"assets/index-abc.js":   {Data: script},
		"pdfjs/wasm/tiny.wasm":  {Data: []byte{0, 'a', 's', 'm'}},
		"assets/worker-abc.mjs": {Data: []byte("export {}")},
		"assets/font-abc.woff2": {Data: []byte("wOF2")},
		"pdfjs/LICENSE":         {Data: []byte("license text")},
		"guide/index.html":      {Data: []byte("<!doctype html><title>Guide</title>")},
		"assets/index.html":     {Data: []byte("<!doctype html><title>Assets</title>")},
		"404.html":              {Data: []byte("<!doctype html><title>Missing</title>")},
	}, "assets")
	if err != nil {
		t.Fatal(err)
	}
	serve := func(method, target string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, nil)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}

	index := serve(http.MethodGet, "/", nil)
	if index.Code != http.StatusOK || index.Header().Get("Cache-Control") != RevalidateCache || !strings.HasPrefix(index.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("index: status=%d headers=%v", index.Code, index.Header())
	}
	if revalidated := serve(http.MethodGet, "/index.html", map[string]string{"If-None-Match": index.Header().Get("ETag")}); revalidated.Code != http.StatusNotModified {
		t.Fatalf("index revalidation status = %d", revalidated.Code)
	}

	compressed := serve(http.MethodGet, "/assets/index-abc.js", map[string]string{"Accept-Encoding": "gzip, br"})
	if compressed.Header().Get("Cache-Control") != ImmutableCache || compressed.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("hashed asset headers = %v", compressed.Header())
	}
	reader, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, _ := io.ReadAll(reader); !bytes.Equal(decoded, script) {
		t.Fatal("gzip body does not match source")
	}

	tiny := serve(http.MethodGet, "/pdfjs/wasm/tiny.wasm", map[string]string{"Accept-Encoding": "gzip"})
	if tiny.Header().Get("Content-Encoding") != "" || tiny.Header().Get("Vary") != "" || tiny.Header().Get("Content-Type") != "application/wasm" {
		t.Fatalf("incompressible asset headers = %v", tiny.Header())
	}
	if tiny.Header().Get("Cache-Control") != RevalidateCache {
		t.Fatal("assets outside the hashed directory must revalidate")
	}

	// 响应类型按固定扩展名表决定，未列出的扩展名按内容嗅探。
	for target, want := range map[string]string{
		"/assets/worker-abc.mjs": "text/javascript; charset=utf-8",
		"/assets/font-abc.woff2": "font/woff2",
		"/pdfjs/LICENSE":         "text/plain; charset=utf-8",
	} {
		if got := serve(http.MethodGet, target, nil).Header().Get("Content-Type"); got != want {
			t.Fatalf("%s content type = %q, want %q", target, got, want)
		}
	}
	// 目录路径返回该目录下的 index.html。
	for _, target := range []string{"/guide/", "/guide"} {
		if directory := serve(http.MethodGet, target, nil); directory.Code != http.StatusOK || directory.Body.String() != "<!doctype html><title>Guide</title>" {
			t.Fatalf("%s: status=%d body=%q", target, directory.Code, directory.Body.String())
		}
	}
	// 目录索引按实际命中的文件选择缓存策略。
	if directory := serve(http.MethodGet, "/assets", nil); directory.Code != http.StatusOK || directory.Header().Get("Cache-Control") != ImmutableCache {
		t.Fatalf("/assets: status=%d headers=%v", directory.Code, directory.Header())
	}

	// 目录下没有 index.html 时返回 404。
	if directory := serve(http.MethodGet, "/pdfjs/", nil); directory.Code != http.StatusNotFound {
		t.Fatalf("/pdfjs/: status=%d", directory.Code)
	}

	// 设置 404 内容后，未命中路径返回该内容与 404 状态。
	if err := server.SetNotFound("404.html"); err != nil {
		t.Fatal(err)
	}
	if missing := serve(http.MethodGet, "/missing", nil); missing.Code != http.StatusNotFound || missing.Body.String() != "<!doctype html><title>Missing</title>" || missing.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("not found page: status=%d headers=%v body=%q", missing.Code, missing.Header(), missing.Body.String())
	}
	if err := server.SetNotFound("absent.html"); err == nil {
		t.Fatal("setting an absent not found page must fail")
	}
	if cleaned := serve(http.MethodGet, "//assets/./x/../index-abc.js", nil); cleaned.Code != http.StatusOK {
		t.Fatalf("uncleaned path status = %d", cleaned.Code)
	}

	head := serve(http.MethodHead, "/assets/index-abc.js", nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "4000" {
		t.Fatalf("head: status=%d bytes=%d length=%q", head.Code, head.Body.Len(), head.Header().Get("Content-Length"))
	}
	if missing := serve(http.MethodGet, "/assets/missing.js", nil); missing.Code != http.StatusNotFound || missing.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing asset: status=%d cache-control=%q", missing.Code, missing.Header().Get("Cache-Control"))
	}
	if posted := serve(http.MethodPost, "/", nil); posted.Code != http.StatusMethodNotAllowed || posted.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("post: status=%d cache-control=%q", posted.Code, posted.Header().Get("Cache-Control"))
	}
}

// TestNewFileServerRequiresIndex 验证缺少 index.html 时拒绝创建。
func TestNewFileServerRequiresIndex(t *testing.T) {
	if _, err := NewFileServer(fstest.MapFS{"assets/app.js": {Data: []byte("x")}}, "assets"); err == nil {
		t.Fatal("expected missing index.html error")
	}
}
