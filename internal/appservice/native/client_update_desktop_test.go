//go:build !server && ((darwin && !ios) || windows)

package native

import (
	"archive/zip"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

// updaterHost 是不打开窗口、不退出进程的更新器宿主。
type updaterHost struct{}

// Emit 丢弃更新器事件。
func (updaterHost) Emit(string, ...any) bool { return true }

// OnEvent 不登记事件监听。
func (updaterHost) OnEvent(string, func(any)) func() { return func() {} }

// OpenWindow 不打开更新窗口。
func (updaterHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return nil }

// Quit 不退出进程。
func (updaterHost) Quit() {}

// updateServer 启动提供客户端目录的服务器：目录中有当前平台的 2.0.0 更新包，内容为单个可执行文件，用 signer 签名。
func updateServer(t *testing.T, signer ed25519.PrivateKey, trusted ed25519.PublicKey) *httptest.Server {
	t.Helper()
	directory := t.TempDir()
	name := "app_2.0.0_update.zip"
	archive, err := os.Create(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	entry, _ := writer.Create("app")
	_, _ = entry.Write([]byte("new binary"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	_ = archive.Close()
	content, _ := os.ReadFile(filepath.Join(directory, name))
	short, long := sha256.Sum256(content), sha512.Sum512(content)
	signature, _ := signer.Sign(nil, long[:], &ed25519.Options{Hash: crypto.SHA512})
	data, _ := json.Marshal(clientrelease.Index{Version: "2.0.0", Updates: []clientrelease.Update{{
		OS: runtime.GOOS, Arch: runtime.GOARCH, Name: name, Size: int64(len(content)),
		SHA256: hex.EncodeToString(short[:]), Signature: base64.StdEncoding.EncodeToString(signature),
	}}})
	if err := os.WriteFile(filepath.Join(directory, clientrelease.IndexName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	catalog, err := clientrelease.Load(directory, "2.0.0", trusted)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(catalog)
	t.Cleanup(server.Close)
	return server
}

// newTestUpdater 创建以 version 运行、信任 publicKey、从 serverURL 更新的桌面端更新能力。
func newTestUpdater(t *testing.T, version string, publicKey ed25519.PublicKey, serverURL string) *clientUpdater {
	t.Helper()
	instance := updater.New(updaterHost{})
	if err := instance.Init(updaterConfig(version, publicKey, func(context.Context) (string, error) { return serverURL, nil })); err != nil {
		t.Fatal(err)
	}
	return &clientUpdater{updater: instance, allowQuit: func(bool) {}}
}

// TestPrepareClientUpdate 验证服务器版本较新时下载并校验更新包，同一版本不重复下载；客户端已是该版本或未连接服务器时没有更新。
func TestPrepareClientUpdate(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	server := updateServer(t, private, public)
	ctx := context.Background()

	client := newTestUpdater(t, "1.0.0", public, server.URL)
	for range 2 {
		update, err := client.PrepareClientUpdate(ctx, appservice.RequestMeta{})
		if err != nil || update.State != appservice.ClientUpdateStateReady || update.Version != "2.0.0" {
			t.Fatalf("准备更新 = %+v, %v", update, err)
		}
	}
	staged, err := os.ReadFile(client.updater.(*updater.Updater).DownloadedPath())
	if err != nil || string(staged) != "new binary" {
		t.Fatalf("解包后的程序 = %q, %v", staged, err)
	}

	for version, serverURL := range map[string]string{"2.0.0": server.URL, "1.0.0": ""} {
		update, err := newTestUpdater(t, version, public, serverURL).PrepareClientUpdate(ctx, appservice.RequestMeta{})
		if err != nil || update.State != appservice.ClientUpdateStateCurrent {
			t.Errorf("版本 %s、服务器 %q 准备更新 = %+v, %v", version, serverURL, update, err)
		}
	}
}

// TestPrepareClientUpdateRejectsUntrustedPackage 验证更新包签名不是由客户端信任的公钥给出，或清单缺少签名时不安装。
func TestPrepareClientUpdateRejectsUntrustedPackage(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	trusted, _, _ := ed25519.GenerateKey(rand.Reader)
	server := updateServer(t, private, public)
	ctx := context.Background()
	client := newTestUpdater(t, "1.0.0", trusted, server.URL)
	if update, err := client.PrepareClientUpdate(ctx, appservice.RequestMeta{}); err == nil {
		t.Fatalf("其他密钥签名的更新包应被拒绝，结果 = %+v", update)
	}
	if err := client.RestartClientUpdate(ctx, appservice.RequestMeta{}); err == nil {
		t.Fatal("没有已准备的新版本时不应重启")
	}

	unsigned := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schemaVersion":1,"version":"2.0.0","artifacts":[{"url":"app.zip","size":1}]}`))
	}))
	t.Cleanup(unsigned.Close)
	if update, err := newTestUpdater(t, "1.0.0", trusted, unsigned.URL).PrepareClientUpdate(ctx, appservice.RequestMeta{}); err == nil {
		t.Fatalf("缺少签名的更新包应被拒绝，结果 = %+v", update)
	}
}
