//go:build server

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLiveness 验证存活探针输出状态且限制请求方法。
func TestLiveness(t *testing.T) {
	service := NewLiveness()
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("存活探针响应 = %d %q", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	service.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 状态码 = %d", recorder.Code)
	}
}
