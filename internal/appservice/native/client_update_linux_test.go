//go:build !server && linux && !android

package native

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
)

// TestLinuxPrepareClientUpdate 验证服务器提供的版本较新时返回 available，版本相同、服务器不提供客户端或未连接服务器时返回 current。
func TestLinuxPrepareClientUpdate(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "1.0.0"
	t.Cleanup(func() { buildinfo.Version = original })
	offered := "2.0.0"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if offered == "" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = writer.Write([]byte(`{"schemaVersion":1,"version":"` + offered + `","artifacts":[]}`))
	}))
	defer server.Close()
	for _, test := range []struct {
		offered, serverURL string
		state              appservice.ClientUpdateState
	}{
		{"2.0.0", server.URL, appservice.ClientUpdateStateAvailable},
		{"1.0.0", server.URL, appservice.ClientUpdateStateCurrent},
		{"", server.URL, appservice.ClientUpdateStateCurrent},
		{"2.0.0", "", appservice.ClientUpdateStateCurrent},
	} {
		offered = test.offered
		serverURL := test.serverURL
		client := NewClientUpdater(nil, func(context.Context) (string, error) { return serverURL, nil }, nil)
		update, err := client.PrepareClientUpdate(context.Background(), appservice.RequestMeta{})
		if err != nil || update.State != test.state || (test.state == appservice.ClientUpdateStateAvailable && update.Version != test.offered) {
			t.Errorf("服务器版本 %q、地址 %q 准备更新 = %+v, %v", test.offered, test.serverURL, update, err)
		}
	}
}
