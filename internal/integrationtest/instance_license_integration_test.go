//go:build server

package integrationtest

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"uuid"
)

// testLicenseKID 是测试签名公钥的编号。
const testLicenseKID = "k-test"

// signTestLicense 用测试私钥为实例签发授权码。
func signTestLicense(t *testing.T, key ed25519.PrivateKey, instanceID string, issuedAt, expiresAt time.Time, capabilities map[string]any) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": license.Issuer, "aud": license.ProductID, "sub": instanceID,
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

// TestInstanceLicenseActivation 验证授权码验签与实例校验、按签发时间替换、能力开放与到期后回到免费取值。
func TestInstanceLicenseActivation(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { brand.EnableOverrideUntil(time.Time{}) })
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	db := openEmptyDatabase(t)
	backend := direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL, LicenseKeys: license.Keys{testLicenseKID: publicKey}},
		nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
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
	overview, err := backend.GetDeploymentOverview(ctx, adminMeta)
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.GetInstanceLicense(ctx, adminMeta)
	if err != nil || current.Status != appservice.LicenseStatusNone || current.ExpiresAt != nil || current.Capabilities.WorkspaceLimit != 1 {
		t.Fatalf("initial license = %#v, err = %v", current, err)
	}

	now := time.Now()
	unlimited := map[string]any{license.CapabilityWorkspaceLimit: 0, license.CapabilityCustomBranding: true}
	// 格式错误、他人签名、其他实例与已过期的授权码都被拒绝。
	_, otherKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	rejected := []struct {
		code string
		key  i18n.Key
	}{
		{"not-a-license", i18n.ErrorInstanceLicenseInvalid},
		{signTestLicense(t, otherKey, overview.InstanceID, now, now.AddDate(1, 0, 0), unlimited), i18n.ErrorInstanceLicenseInvalid},
		{signTestLicense(t, privateKey, uuid.NewV7().String(), now, now.AddDate(1, 0, 0), unlimited), i18n.ErrorInstanceLicenseInstanceMismatch},
		{signTestLicense(t, privateKey, overview.InstanceID, now.AddDate(-1, 0, 0), now.Add(-time.Minute), unlimited), i18n.ErrorInstanceLicenseExpired},
	}
	for _, item := range rejected {
		_, err := backend.ActivateInstanceLicense(ctx, adminMeta, appservice.ActivateInstanceLicenseInput{LicenseCode: item.code})
		requireErrorMessage(t, err, item.key)
	}

	// 有效授权开放多工作区与自定义品牌，重复提交同一授权码原样返回。
	code := signTestLicense(t, privateKey, overview.InstanceID, now.Add(-time.Hour), now.AddDate(1, 0, 0), unlimited)
	activated, err := service.ActivateInstanceLicense(ctx, adminMeta, appservice.ActivateInstanceLicenseInput{LicenseCode: "\n" + code + " "})
	if err != nil || activated.Status != appservice.LicenseStatusActive || activated.Customer != "测试客户" || activated.ExpiresAt == nil ||
		activated.Capabilities.WorkspaceLimit != 0 || !activated.Capabilities.CustomBranding {
		t.Fatalf("activated = %#v, err = %v", activated, err)
	}
	if !brand.OverrideActive() {
		t.Fatal("授予自定义品牌后部署品牌配置应生效")
	}
	again, err := backend.ActivateInstanceLicense(ctx, adminMeta, appservice.ActivateInstanceLicenseInput{LicenseCode: code})
	if err != nil || again.LicenseID != activated.LicenseID {
		t.Fatalf("again = %#v, err = %v", again, err)
	}
	if _, err := backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: "第二工作区", Slug: "license-second"}); err != nil {
		t.Fatalf("create second workspace: %v", err)
	}

	// 签发时间不晚于当前授权的授权码被拒绝，更晚的授权码替换当前授权。
	older := signTestLicense(t, privateKey, overview.InstanceID, now.Add(-2*time.Hour), now.AddDate(2, 0, 0), unlimited)
	_, err = backend.ActivateInstanceLicense(ctx, adminMeta, appservice.ActivateInstanceLicenseInput{LicenseCode: older})
	requireErrorMessage(t, err, i18n.ErrorInstanceLicenseSuperseded)
	newer := signTestLicense(t, privateKey, overview.InstanceID, now, now.AddDate(2, 0, 0), map[string]any{license.CapabilityWorkspaceLimit: 2})
	replaced, err := backend.ActivateInstanceLicense(ctx, adminMeta, appservice.ActivateInstanceLicenseInput{LicenseCode: newer})
	if err != nil || replaced.LicenseID == activated.LicenseID || replaced.Capabilities.WorkspaceLimit != 2 || replaced.Capabilities.CustomBranding {
		t.Fatalf("replaced = %#v, err = %v", replaced, err)
	}
	if brand.OverrideActive() {
		t.Fatal("新授权未授予自定义品牌时部署品牌配置不应生效")
	}
	_, err = backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: "第三工作区", Slug: "license-third"})
	requireErrorKind(t, err, appservice.ErrorKindConflict)

	// 授权到期后保留授权记录，实例按免费取值运行。
	if _, err := db.ExecContext(ctx, "UPDATE instance_licenses SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	expired, err := backend.GetInstanceLicense(ctx, adminMeta)
	if err != nil || expired.Status != appservice.LicenseStatusExpired || expired.LicenseID != replaced.LicenseID {
		t.Fatalf("expired = %#v, err = %v", expired, err)
	}
	overview, err = backend.GetDeploymentOverview(ctx, adminMeta)
	if err != nil || overview.Capabilities.WorkspaceLimit != 1 {
		t.Fatalf("expired overview = %#v, err = %v", overview, err)
	}
}
