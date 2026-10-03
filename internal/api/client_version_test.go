//go:build server

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
)

// TestClientVersionMiddleware 验证接口版本过旧的原生端请求返回需要升级的会话错误，未携带版本或版本兼容的请求继续处理。
func TestClientVersionMiddleware(t *testing.T) {
	handler := ClientVersionMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	cases := map[string]int{
		"": http.StatusNoContent,
		strconv.Itoa(appservice.MinClientAPIVersion):     http.StatusNoContent,
		strconv.Itoa(appservice.MinClientAPIVersion - 1): http.StatusPreconditionFailed,
	}
	for version, want := range cases {
		request := httptest.NewRequest(http.MethodGet, "/api/auth/identity", nil)
		if version != "" {
			request.Header.Set(appservice.ClientAPIVersionHeader, version)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Fatalf("version %q status = %d, want %d", version, recorder.Code, want)
		}
		if want != http.StatusPreconditionFailed {
			continue
		}
		var body struct {
			Error struct {
				State appservice.SessionState `json:"state"`
			} `json:"error"`
		}
		if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil || body.Error.State != appservice.SessionStateUpgrade {
			t.Fatalf("version %q body state = %q, err = %v", version, body.Error.State, err)
		}
	}
}
