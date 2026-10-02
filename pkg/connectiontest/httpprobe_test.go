package connectiontest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReadHTTPResponseRejectsOversizedBody 验证超出读取上限的响应按协议错误处理且不调用解码。
func TestReadHTTPResponseRejectsOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(writer, strings.NewReader(strings.Repeat("a", maxResponseBytes+1)))
	}))
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded := false
	err = ReadHTTPResponse(context.Background(), server.Client(), request, func(io.Reader) error {
		decoded = true
		return nil
	})
	stage, kind, ok := Details(err)
	if !ok || stage != StageCapability || kind != FailureProtocol || decoded {
		t.Fatalf("stage = %q, kind = %q, ok = %v, decoded = %v", stage, kind, ok, decoded)
	}
}
