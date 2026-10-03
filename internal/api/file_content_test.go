//go:build server

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
)

// TestLocalObjectStorageKey 验证本地对象服务只接受规范 storage_key。
func TestLocalObjectStorageKey(t *testing.T) {
	requestPath := "organizations/00000000-0000-0000-0000-000000000001/files/00000000-0000-0000-0000-000000000002.png"
	storageKey, ok := localObjectStorageKey(requestPath)
	if !ok || storageKey != requestPath {
		t.Fatalf("localObjectStorageKey() = %q, %v", storageKey, ok)
	}
	for _, invalid := range []string{
		"organizations/org/files/file.png",
		"organizations/00000000-0000-0000-0000-000000000001/files/../file.png",
		"organizations/00000000-0000-0000-0000-000000000001/files/00000000-0000-0000-0000-000000000002",
	} {
		if _, ok := localObjectStorageKey(invalid); ok {
			t.Fatalf("expected %q to fail", invalid)
		}
	}
}

// TestLocalObjectServiceServesFinalObjects 验证本地读取直接使用最终对象目录的静态文件服务。
func TestLocalObjectServiceServesFinalObjects(t *testing.T) {
	store, err := serverfilecontent.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storageKey := "organizations/00000000-0000-0000-0000-000000000001/files/00000000-0000-0000-0000-000000000002.png"
	if err := store.Save(context.Background(), storageKey, strings.NewReader("avatar"), 6); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/"+storageKey, nil)
	NewLocalObjectService(nil, store).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "avatar" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != serverfilecontent.ImmutableCacheControl {
		t.Fatalf("cache control = %q", response.Header().Get("Cache-Control"))
	}

	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodGet, "/organizations/00000000-0000-0000-0000-000000000001/files/00000000-0000-0000-0000-000000000003.png", nil)
	NewLocalObjectService(nil, store).ServeHTTP(missing, missingRequest)
	if missing.Code != http.StatusNotFound || missing.Header().Get("Cache-Control") != "" {
		t.Fatalf("missing response = %d, cache control %q", missing.Code, missing.Header().Get("Cache-Control"))
	}
}

// TestLocalObjectServiceForcesSafeContentType 验证只有可内嵌展示的图片按图片类型输出，其余文件一律按附件下载并在沙箱中打开，多段范围请求不改写内容类型。
func TestLocalObjectServiceForcesSafeContentType(t *testing.T) {
	store, err := serverfilecontent.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ extension, contentType, disposition string }{
		{".png", "image/png", ""},
		{".html", "application/octet-stream", "attachment"},
		{".svg", "application/octet-stream", "attachment"},
	} {
		storageKey := "organizations/00000000-0000-0000-0000-000000000001/files/00000000-0000-0000-0000-000000000002" + item.extension
		if err := store.Save(context.Background(), storageKey, strings.NewReader("<script>alert(1)</script>"), 25); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/"+storageKey, nil)
		request.Header.Set("Range", "bytes=0-1,3-4")
		NewLocalObjectService(nil, store).ServeHTTP(response, request)
		header := response.Header()
		if response.Code != http.StatusOK || header.Get("Content-Type") != item.contentType || header.Get("Content-Disposition") != item.disposition ||
			header.Get("Content-Security-Policy") != "sandbox" || header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s response = %d %v", item.extension, response.Code, header)
		}
	}
}
