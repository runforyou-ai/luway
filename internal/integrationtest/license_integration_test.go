//go:build server

package integrationtest

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/control"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"uuid"
)

// testLicenseKID 是测试签名公钥的编号。
const testLicenseKID = "k-test"

// signTestLicense 用测试私钥为服务器签发授权码。
func signTestLicense(t *testing.T, key ed25519.PrivateKey, serverID string, issuedAt, expiresAt time.Time, capabilities map[string]any) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": license.Issuer, "aud": license.ProductID, "sub": serverID,
		"iat": issuedAt.Unix(), "exp": expiresAt.Unix(),
		"license_id": uuid.NewV7().String(), "customer": "测试客户", "capabilities": capabilities,
	})
	token.Header["typ"] = "license+jwt"
	token.Header["kid"] = testLicenseKID
	code, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// requireErrorMessage 断言错误是指定文案的业务错误。
func requireErrorMessage(t *testing.T, err error, key i18n.Key) {
	t.Helper()
	want, _ := i18n.Localize("zh-CN", key)
	if appErr, ok := errors.AsType[*appservice.Error](err); !ok || appErr.Message != want {
		t.Fatalf("error = %#v, want message %q", err, want)
	}
}

// TestLicenseActivation 验证授权码验签与服务器标识校验、按签发时间替换、能力开放与到期后回到免费取值。
func TestLicenseActivation(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { brand.EnableOverrideUntil(time.Time{}) })
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	db := openEmptyDatabase(t)
	backend := direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL, LicenseKeys: license.Keys{testLicenseKID: publicKey}},
		nil, serverfilecontent.S3Config{}, nil, nil, newTestTasks(db), nil, nil, nil)
	service := appservice.New(backend)
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}

	admin, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		WorkspaceName: "授权测试", WorkspaceSlug: "license-test", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: appservice.LocaleChineseSimplified}
	overview, err := backend.GetPlatformOverview(ctx, adminMeta)
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.GetLicense(ctx, adminMeta)
	if err != nil || current.Status != appservice.LicenseStatusNone || current.ExpiresAt != nil || current.Capabilities.WorkspaceLimit != 1 {
		t.Fatalf("initial license = %#v, err = %v", current, err)
	}

	now := time.Now()
	unlimited := map[string]any{license.CapabilityWorkspaceLimit: 0, license.CapabilityCustomBranding: true}
	// 格式错误、他人签名、其他服务器与已过期的授权码都被拒绝。
	_, otherKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	rejected := []struct {
		code string
		key  i18n.Key
	}{
		{"not-a-license", i18n.ErrorLicenseInvalid},
		{signTestLicense(t, otherKey, overview.ServerID, now, now.AddDate(1, 0, 0), unlimited), i18n.ErrorLicenseInvalid},
		{signTestLicense(t, privateKey, uuid.NewV7().String(), now, now.AddDate(1, 0, 0), unlimited), i18n.ErrorLicenseServerMismatch},
		{signTestLicense(t, privateKey, overview.ServerID, now.AddDate(-1, 0, 0), now.Add(-time.Minute), unlimited), i18n.ErrorLicenseExpired},
	}
	for _, item := range rejected {
		_, err := backend.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: item.code})
		requireErrorMessage(t, err, item.key)
	}

	// 有效授权开放多工作区与自定义品牌，重复提交同一授权码原样返回。
	code := signTestLicense(t, privateKey, overview.ServerID, now.Add(-time.Hour), now.AddDate(1, 0, 0), unlimited)
	activated, err := service.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: "\n" + code + " "})
	if err != nil || activated.Status != appservice.LicenseStatusActive || activated.Customer != "测试客户" || activated.ExpiresAt == nil ||
		activated.Capabilities.WorkspaceLimit != 0 || !activated.Capabilities.CustomBranding {
		t.Fatalf("activated = %#v, err = %v", activated, err)
	}
	if !brand.OverrideActive() {
		t.Fatal("授予自定义品牌后部署品牌配置应生效")
	}
	again, err := backend.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: code})
	if err != nil || again.LicenseID != activated.LicenseID {
		t.Fatalf("again = %#v, err = %v", again, err)
	}
	if _, err := backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: "第二工作区", Slug: "license-second"}); err != nil {
		t.Fatalf("create second workspace: %v", err)
	}

	// 签发时间不晚于当前授权的授权码被拒绝，更晚的授权码替换当前授权。
	older := signTestLicense(t, privateKey, overview.ServerID, now.Add(-2*time.Hour), now.AddDate(2, 0, 0), unlimited)
	_, err = backend.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: older})
	requireErrorMessage(t, err, i18n.ErrorLicenseSuperseded)
	newer := signTestLicense(t, privateKey, overview.ServerID, now, now.AddDate(2, 0, 0), map[string]any{license.CapabilityWorkspaceLimit: 2})
	replaced, err := backend.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: newer})
	if err != nil || replaced.LicenseID == activated.LicenseID || replaced.Capabilities.WorkspaceLimit != 2 || replaced.Capabilities.CustomBranding {
		t.Fatalf("replaced = %#v, err = %v", replaced, err)
	}
	if brand.OverrideActive() {
		t.Fatal("新授权未授予自定义品牌时部署品牌配置不应生效")
	}
	_, err = backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: "第三工作区", Slug: "license-third"})
	requireErrorKind(t, err, appservice.ErrorKindConflict)

	// 授权到期后保留授权记录，平台按免费取值运行。
	if _, err := db.ExecContext(ctx, "UPDATE licenses SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	expired, err := backend.GetLicense(ctx, adminMeta)
	if err != nil || expired.Status != appservice.LicenseStatusExpired || expired.LicenseID != replaced.LicenseID {
		t.Fatalf("expired = %#v, err = %v", expired, err)
	}
	overview, err = backend.GetPlatformOverview(ctx, adminMeta)
	if err != nil || overview.Capabilities.WorkspaceLimit != 1 {
		t.Fatalf("expired overview = %#v, err = %v", overview, err)
	}
}

// fakeControl 是记录请求并按预设返回授权码的 control 测试替身。
type fakeControl struct {
	mu          sync.Mutex
	serverID    string
	registered  int
	licenseCode string
	issue       func() string
}

// ServeHTTP 校验签名请求头与服务器身份后，按路径模拟登记、激活和拉取授权。
func (f *fakeControl) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if request.Header.Get("Signature") == "" || request.Header.Get("Content-Digest") == "" ||
		!strings.Contains(request.Header.Get("Signature-Input"), `keyid="`+f.serverID+`"`) {
		writeProblem(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	var body struct {
		Product        string `json:"product"`
		PublicKey      string `json:"public_key"`
		ActivationCode string `json:"activation_code"`
	}
	_ = json.NewDecoder(request.Body).Decode(&body)
	switch request.URL.Path {
	case "/api/v1/servers":
		if body.Product != license.ProductID || body.PublicKey == "" {
			writeProblem(writer, http.StatusBadRequest, "validation_failed")
			return
		}
		f.registered++
		writer.WriteHeader(http.StatusNoContent)
	case "/api/v1/activations":
		switch body.ActivationCode {
		case "USED-USED-USED-USED":
			writeProblem(writer, http.StatusConflict, "server_mismatch")
		case "GOOD-GOOD-GOOD-GOOD":
			f.licenseCode = f.issue()
			_ = json.NewEncoder(writer).Encode(map[string]string{"license_code": f.licenseCode})
		default:
			writeProblem(writer, http.StatusNotFound, "activation_code_invalid")
		}
	case "/api/v1/license":
		if f.licenseCode == "" {
			writeProblem(writer, http.StatusNotFound, "not_found")
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"license_code": f.licenseCode})
	default:
		writeProblem(writer, http.StatusNotFound, "not_found")
	}
}

// writeProblem 写入 RFC 9457 错误响应。
func writeProblem(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"type": "about:blank", "title": code, "status": status, "code": code, "retryable": false})
}

// TestLicenseOnline 验证经 control 在线激活、后台同步续期、错误映射、上报开关与服务器标识重置。
func TestLicenseOnline(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { brand.EnableOverrideUntil(time.Time{}) })
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	db := openEmptyDatabase(t)
	keys := license.Keys{testLicenseKID: publicKey}
	fake := &fakeControl{}
	server := httptest.NewServer(fake)
	defer server.Close()
	client := control.New(server.URL, "test", func(ctx context.Context) (control.Identity, error) {
		return platformaction.ControlIdentity(ctx, db)
	})
	backend := direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL, LicenseKeys: keys, Control: client},
		nil, serverfilecontent.S3Config{}, nil, nil, newTestTasks(db), nil, nil, nil)
	service := appservice.New(backend)
	online := platformaction.NewOnlineLicenseAction(db, keys, client)
	ctx := context.Background()

	// 未安装时后台同步直接跳过。
	if err := online.SyncTask(ctx, platformaction.SyncLicenseInput{}); err != nil {
		t.Fatalf("sync before install: %v", err)
	}
	admin, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		WorkspaceName: "在线授权", WorkspaceSlug: "online-license", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: appservice.LocaleChineseSimplified}
	current, err := service.GetLicense(ctx, adminMeta)
	if err != nil || current.ServerID == "" || current.Status != appservice.LicenseStatusNone {
		t.Fatalf("initial license = %#v, err = %v", current, err)
	}
	serverID := current.ServerID
	now := time.Now()
	fake.mu.Lock()
	fake.serverID = serverID
	fake.issue = func() string {
		return signTestLicense(t, privateKey, serverID, now.Add(-time.Hour), now.AddDate(1, 0, 0), map[string]any{license.CapabilityWorkspaceLimit: 0})
	}
	fake.mu.Unlock()

	// control 中尚无授权时：手动同步提示先激活，后台同步保持现状。
	_, err = backend.SyncLicense(ctx, adminMeta)
	requireErrorMessage(t, err, i18n.ErrorLicenseNotIssued)
	if err := online.SyncTask(ctx, platformaction.SyncLicenseInput{}); err != nil {
		t.Fatalf("sync without license: %v", err)
	}

	// 激活码无效或已用于其他服务器时返回对应错误，有效激活码激活授权。
	_, err = backend.ActivateLicenseOnline(ctx, adminMeta, appservice.ActivateLicenseOnlineInput{ActivationCode: "BAD"})
	requireErrorMessage(t, err, i18n.ErrorActivationCodeInvalid)
	_, err = backend.ActivateLicenseOnline(ctx, adminMeta, appservice.ActivateLicenseOnlineInput{ActivationCode: "USED-USED-USED-USED"})
	requireErrorMessage(t, err, i18n.ErrorActivationServerMismatch)
	activated, err := service.ActivateLicenseOnline(ctx, adminMeta, appservice.ActivateLicenseOnlineInput{ActivationCode: " GOOD-GOOD-GOOD-GOOD\n"})
	if err != nil || activated.Status != appservice.LicenseStatusActive || activated.ServerID != serverID || activated.Capabilities.WorkspaceLimit != 0 {
		t.Fatalf("activated = %#v, err = %v", activated, err)
	}

	// control 续期后后台同步替换为签发更晚的授权码；control 返回较早的授权码时保留本地授权。
	fake.mu.Lock()
	renewed := signTestLicense(t, privateKey, serverID, now, now.AddDate(2, 0, 0), map[string]any{license.CapabilityWorkspaceLimit: 0})
	fake.licenseCode = renewed
	fake.mu.Unlock()
	if err := online.SyncTask(ctx, platformaction.SyncLicenseInput{}); err != nil {
		t.Fatalf("sync renewed: %v", err)
	}
	synced, err := service.GetLicense(ctx, adminMeta)
	if err != nil || synced.ExpiresAt == nil || !synced.ExpiresAt.After(activated.ExpiresAt.AddDate(0, 6, 0)) {
		t.Fatalf("synced = %#v, err = %v", synced, err)
	}
	fake.mu.Lock()
	fake.licenseCode = signTestLicense(t, privateKey, serverID, now.Add(-2*time.Hour), now.AddDate(3, 0, 0), map[string]any{})
	registered := fake.registered
	fake.mu.Unlock()
	kept, err := service.SyncLicense(ctx, adminMeta)
	if err != nil || !kept.ExpiresAt.Equal(*synced.ExpiresAt) || kept.ServerID != serverID {
		t.Fatalf("kept = %#v, err = %v", kept, err)
	}
	fake.mu.Lock()
	if fake.registered != registered+1 {
		t.Fatalf("registered = %d, want %d", fake.registered, registered+1)
	}
	fake.mu.Unlock()

	// control 不再有授权或只返回已到期的授权码时，手动同步保留本地有效授权；查不到授权时记录起始时间，再次查到授权码时清空。
	for _, code := range []string{"", "", signTestLicense(t, privateKey, serverID, now.AddDate(-2, 0, 0), now.Add(-time.Hour), map[string]any{})} {
		fake.mu.Lock()
		fake.licenseCode = code
		fake.mu.Unlock()
		local, err := service.SyncLicense(ctx, adminMeta)
		if err != nil || local.Status != appservice.LicenseStatusActive || !local.ExpiresAt.Equal(*synced.ExpiresAt) {
			t.Fatalf("local = %#v, err = %v", local, err)
		}
		if (code == "") != (local.ControlMissingAt != nil) {
			t.Fatalf("control missing at = %v for code %q", local.ControlMissingAt, code)
		}
	}

	// 后台同步查不到授权时保留首次查不到的时间。
	fake.mu.Lock()
	fake.licenseCode = ""
	fake.mu.Unlock()
	if err := online.SyncTask(ctx, platformaction.SyncLicenseInput{}); err != nil {
		t.Fatalf("sync missing: %v", err)
	}
	missing, err := service.GetLicense(ctx, adminMeta)
	if err != nil || missing.ControlMissingAt == nil {
		t.Fatalf("missing = %#v, err = %v", missing, err)
	}
	if err := online.SyncTask(ctx, platformaction.SyncLicenseInput{}); err != nil {
		t.Fatalf("sync missing again: %v", err)
	}
	stillMissing, err := service.GetLicense(ctx, adminMeta)
	if err != nil || stillMissing.ControlMissingAt == nil || !stillMissing.ControlMissingAt.Equal(*missing.ControlMissingAt) {
		t.Fatalf("still missing = %#v, err = %v", stillMissing, err)
	}

	// 离线替换授权码时清空查不到授权的记录。
	replaced, err := service.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{
		LicenseCode: signTestLicense(t, privateKey, serverID, now.Add(time.Minute), now.AddDate(2, 0, 0), map[string]any{license.CapabilityWorkspaceLimit: 0}),
	})
	if err != nil || replaced.ControlMissingAt != nil {
		t.Fatalf("replaced = %#v, err = %v", replaced, err)
	}

	// 在线激活取得不晚于当前授权的授权码时同样清空查不到授权的记录。
	if err := online.SyncTask(ctx, platformaction.SyncLicenseInput{}); err != nil {
		t.Fatalf("sync missing before activation: %v", err)
	}
	reactivated, err := service.ActivateLicenseOnline(ctx, adminMeta, appservice.ActivateLicenseOnlineInput{ActivationCode: "GOOD-GOOD-GOOD-GOOD"})
	if err != nil || reactivated.ControlMissingAt != nil || !reactivated.ExpiresAt.Equal(*replaced.ExpiresAt) {
		t.Fatalf("reactivated = %#v, err = %v", reactivated, err)
	}

	// 本地授权已到期且 control 查不到授权时，手动同步返回本地授权并带上查不到授权的记录。
	if _, err := db.NewUpdate().Table("licenses").Set("expires_at = now() - interval '1 hour'").Where("server_id = ?", serverID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.licenseCode = ""
	fake.mu.Unlock()
	expiredMissing, err := service.SyncLicense(ctx, adminMeta)
	if err != nil || expiredMissing.Status != appservice.LicenseStatusExpired || expiredMissing.ControlMissingAt == nil {
		t.Fatalf("expired missing = %#v, err = %v", expiredMissing, err)
	}

	// 关闭上报后不再采集运行指标。
	values, err := platformaction.TelemetryMetrics(ctx, db)
	if err != nil || values == nil || values["platform.accounts"] != 1 {
		t.Fatalf("metrics = %#v, err = %v", values, err)
	}
	settings, err := service.UpdatePlatformTelemetry(ctx, adminMeta, appservice.PlatformTelemetryInput{TelemetryEnabled: false})
	if err != nil || settings.TelemetryEnabled {
		t.Fatalf("settings = %#v, err = %v", settings, err)
	}
	if values, err := platformaction.TelemetryMetrics(ctx, db); err != nil || values != nil {
		t.Fatalf("metrics after disable = %#v, err = %v", values, err)
	}

	// control 不可用时提示改用离线授权。
	server.Close()
	_, err = backend.SyncLicense(ctx, adminMeta)
	requireErrorMessage(t, err, i18n.ErrorControlUnavailable)

	// 重置服务器标识生成新的服务器标识并删除本地授权。
	resetID, err := platformaction.ResetServerID(ctx, db)
	if err != nil || resetID == "" || resetID == serverID {
		t.Fatalf("reset = %q, err = %v", resetID, err)
	}
	reset, err := service.GetLicense(ctx, adminMeta)
	if err != nil || reset.ServerID != resetID || reset.Status != appservice.LicenseStatusNone {
		t.Fatalf("after reset = %#v, err = %v", reset, err)
	}
}
