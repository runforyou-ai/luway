//go:build !server && ((darwin && !ios) || windows || (linux && !android))

package native

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

// fakeUpdateServer 是固定服务器地址的应用内更新服务器连接。
type fakeUpdateServer struct{ url string }

// ServerURL 返回固定的服务器地址。
func (s fakeUpdateServer) ServerURL(context.Context, appservice.RequestMeta) (string, error) {
	return s.url, nil
}

// InstallationStatus 返回空的服务器状态。
func (fakeUpdateServer) InstallationStatus(context.Context, appservice.RequestMeta) (appservice.InstallationStatus, error) {
	return appservice.InstallationStatus{}, nil
}

// TestServerProviderRequiresSignature 校验服务器清单中的更新包缺少 Ed25519ph 签名时拒绝更新。
func TestServerProviderRequiresSignature(t *testing.T) {
	artifact := map[string]any{"url": "/clients/app.zip", "filename": "app.zip", "size": 3, "platform": "darwin", "arch": "arm64", "digestAlgo": "sha512", "digest": "AAAA"}
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"schemaVersion": 1, "version": "1.1.0", "artifacts": []any{artifact}})
	}))
	defer remote.Close()
	provider := serverProvider{server: fakeUpdateServer{url: remote.URL}}
	request := updater.CheckRequest{CurrentVersion: "1.0.0", Platform: "darwin", Arch: "arm64"}
	if release, err := provider.Check(context.Background(), request); err == nil {
		t.Fatalf("未签名更新包 = %+v，期望报错", release)
	}
	artifact["signatureAlgo"], artifact["signature"] = "ed25519ph", "c2lnbmF0dXJl"
	if release, err := provider.Check(context.Background(), request); err != nil || release == nil {
		t.Fatalf("已签名更新包 = %+v, %v", release, err)
	}
}
