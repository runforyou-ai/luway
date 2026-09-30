//go:build server

package filecontent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPut 验证服务端导入对象携带与浏览器直传一致的缓存元数据。
func TestPut(t *testing.T) {
	data := []byte("image")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/app/organizations/org/files/file.png" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") == "" || request.Header.Get("Content-Type") != "image/png" || request.Header.Get("Cache-Control") != ImmutableCacheControl {
			t.Errorf("headers = %#v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || !bytes.Equal(body, data) {
			t.Errorf("body = %q, error = %v", body, err)
		}
		writer.Header().Set("ETag", `"etag"`)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := S3Config{
		Endpoint: server.URL, Region: "us-east-1", Bucket: "app",
		AccessKeyID: "access-key", SecretAccessKey: "secret-key", ForcePathStyle: true,
	}
	etag, err := Put(context.Background(), config, "organizations/org/files/file.png", "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if etag != `"etag"` {
		t.Fatalf("etag = %q", etag)
	}
}

// TestPresignRequests 验证对象存储上传请求携带不可变缓存元数据。
func TestPresignRequests(t *testing.T) {
	config := S3Config{
		Endpoint: "https://storage.example.com", Region: "us-east-1", Bucket: "app",
		AccessKeyID: "access-key", SecretAccessKey: "secret-key", ForcePathStyle: true,
	}
	put, err := PresignPut(context.Background(), config, "organizations/org/files/file.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if put.Method != http.MethodPut || !strings.HasPrefix(put.URL, "https://storage.example.com/app/") {
		t.Fatalf("put request = %#v", put)
	}
	if _, exists := put.Headers["Host"]; exists {
		t.Fatalf("browser upload request contains forbidden Host header: %#v", put.Headers)
	}
	if put.Headers["Cache-Control"] != ImmutableCacheControl {
		t.Fatalf("cache control = %q, want %q", put.Headers["Cache-Control"], ImmutableCacheControl)
	}
	if put.Headers["Content-Type"] != "image/png" {
		t.Fatalf("content type = %q, want image/png", put.Headers["Content-Type"])
	}
}

// TestS3ClientReuse 验证同一配置复用客户端，不同配置使用各自的客户端。
func TestS3ClientReuse(t *testing.T) {
	config := S3Config{Enabled: true, Endpoint: "https://s3.reuse.test", Region: "us-east-1", Bucket: "bucket", AccessKeyID: "key", SecretAccessKey: "secret"}
	other := config
	other.Bucket = "other"
	if newS3Client(config) != newS3Client(config) {
		t.Fatal("same config created a new client")
	}
	if newS3Client(config) == newS3Client(other) {
		t.Fatal("different configs shared a client")
	}
}
