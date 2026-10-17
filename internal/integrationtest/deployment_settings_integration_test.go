//go:build server

package integrationtest

import (
	"context"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
)

// TestDeploymentSettings 验证站点设置与部署集成配置：首次安装写入部署地址，平台管理员修改站点设置、邮件发送后本实例立即生效、其他实例刷新后生效，
// 未授权时品牌只读为构建默认且不写库，字段校验与服务端一致，存储桶不可访问时不保存，普通成员无权读取。
func TestDeploymentSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	deployment := servertest.NewDeployment(t, db)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: deployment}, nil, nil, testEnqueuer, nil, nil, nil)
	service := backend
	admin := servertest.InstallPlatform(t, service, appservice.InstallPlatformInput{
		PublicURL: servertest.PublicURL + "/", DisplayName: "管理员", Email: servertest.UniqueEmail("deployment"),
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	adminMeta := servertest.AdminMeta(admin.AdminToken)
	// 另一台服务器的部署状态在刷新前保持旧值。
	other := servertest.NewDeployment(t, db)

	// 首次安装去掉部署地址末尾的斜杠后保存，安装实例立即使用。
	current, err := service.GetPlatformSite(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, servertest.PublicURL, current.PublicURL)
	require.Equal(t, servertest.PublicURL, deployment.PublicURL())
	require.False(t, current.BrandingLicensed)
	require.Equal(t, brand.Build().Names, current.Branding.Names)
	require.Equal(t, brand.Build().SDKName, current.Branding.SDKName)
	require.Empty(t, current.Branding.Icon)
	require.False(t, deployment.Mailer().Enabled())

	// 部署地址必须是不带路径的完整地址；部署中没有直接提供 HTTPS 的服务器时，HTTPS 部署地址不签发证书。
	input := platformSiteInputFrom(current)
	input.PublicURL = "https://support.example.test/app"
	input.Certificate = appservice.PlatformCertificate{Source: appservice.PlatformCertificateSourceACME}
	_, err = service.UpdatePlatformSite(ctx, adminMeta, input)
	servertest.RequireFieldError(t, err, "publicURL", i18n.FieldPublicURLInvalid)
	input.PublicURL = "https://support.example.test/"
	updated, err := service.UpdatePlatformSite(ctx, adminMeta, input)
	require.NoError(t, err)
	require.Equal(t, "https://support.example.test", updated.PublicURL)
	require.False(t, updated.HTTPSServers)
	require.Nil(t, updated.CertificateStatus.ExpiresAt)
	// 部署名称不超过 64 个字符。
	input = platformSiteInputFrom(updated)
	input.Name = " 总部 "
	updated, err = service.UpdatePlatformSite(ctx, adminMeta, input)
	require.NoError(t, err)
	require.Equal(t, "总部", updated.Name)
	require.Equal(t, "https://support.example.test", deployment.PublicURL())
	status, err := service.InstallationStatus(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified})
	require.NoError(t, err)
	require.Equal(t, "总部", status.DeploymentName)
	require.Equal(t, servertest.PublicURL, other.PublicURL(), "other instance before refresh")
	require.NoError(t, other.Reload(ctx))
	require.Equal(t, "https://support.example.test", other.PublicURL())

	// 邮件配置明确启用并按当前服务商校验，读取结果只包含密钥状态。
	_, err = service.UpdatePlatformEmail(ctx, adminMeta, appservice.PlatformEmailInput{Enabled: true, Provider: domain.EmailProviderSMTP, FromAddress: "support", SMTP: &appservice.PlatformEmailSMTPInput{Host: "smtp.example.test", Port: 0, Security: "ssl", Username: "mailer"}})
	servertest.RequireFieldError(t, err, "fromAddress", i18n.FieldEmailInvalid)
	// 发件邮箱合法时返回 SMTP 专属字段错误。
	_, err = service.UpdatePlatformEmail(ctx, adminMeta, appservice.PlatformEmailInput{Enabled: true, Provider: domain.EmailProviderSMTP, FromAddress: "support@example.test", SMTP: &appservice.PlatformEmailSMTPInput{Host: "smtp.example.test", Port: 0, Security: "ssl", Username: "mailer"}})
	servertest.RequireFieldError(t, err, "smtp.port", i18n.FieldPortInvalid)
	servertest.RequireFieldError(t, err, "smtp.security", i18n.FieldSMTPSecurityInvalid)
	servertest.RequireFieldError(t, err, "smtp.password", i18n.FieldSMTPCredentialsIncomplete)
	emailUpdated, err := service.UpdatePlatformEmail(ctx, adminMeta, appservice.PlatformEmailInput{Enabled: true, Provider: domain.EmailProviderSMTP, FromAddress: "support@example.test", SMTP: &appservice.PlatformEmailSMTPInput{Host: " smtp.example.test ", Port: 465, Security: "TLS", Username: "mailer", Password: "secret"}})
	require.NoError(t, err)
	require.Equal(t, &appservice.PlatformEmailSMTPSettings{Host: "smtp.example.test", Port: 465, Security: "tls", Username: "mailer", PasswordSet: true}, emailUpdated.Email.SMTP)
	require.True(t, deployment.Mailer().Enabled())
	_, err = service.UpdatePlatformEmail(ctx, adminMeta, appservice.PlatformEmailInput{Provider: domain.EmailProviderSMTP, SMTP: &appservice.PlatformEmailSMTPInput{Port: 587, Security: "starttls"}})
	require.NoError(t, err)
	require.False(t, deployment.Mailer().Enabled())

	// 未授权时品牌对外为构建默认值，提交自定义品牌不写库、不生效。
	input = loadPlatformSiteInput(t, service, ctx, adminMeta)
	input.Branding = appservice.PlatformBrandingSettings{Names: map[string]string{"zh-CN": "艾克米"}, SDKName: "Acme", Icon: append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)}
	updated, err = service.UpdatePlatformSite(ctx, adminMeta, input)
	require.NoError(t, err)
	require.Equal(t, brand.Build().Names, updated.Branding.Names)
	require.Equal(t, brand.Build().SDKName, updated.Branding.SDKName)
	require.Empty(t, updated.Branding.Icon)
	_, active := deployment.BrandOverride()
	require.False(t, active)
	require.Empty(t, deployment.Current().Settings.Branding.Icon)

	// 授权后可保存品牌；只接受构建品牌已有语言的名称与有效的对象名和 PNG 图标。
	servertest.SeedActiveLicense(t, db)
	require.NoError(t, deployment.Reload(ctx))
	input = loadPlatformSiteInput(t, service, ctx, adminMeta)
	input.Branding = appservice.PlatformBrandingSettings{Names: map[string]string{"fr-FR": "Acme"}, SDKName: "acme-desk", Icon: []byte("not png")}
	_, err = service.UpdatePlatformSite(ctx, adminMeta, input)
	servertest.RequireFieldError(t, err, "names", i18n.FieldBrandNameInvalid)
	servertest.RequireFieldError(t, err, "sdkName", i18n.FieldBrandSDKNameInvalid)
	servertest.RequireFieldError(t, err, "icon", i18n.FieldBrandIconInvalid)
	icon := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)
	input.Branding = appservice.PlatformBrandingSettings{Names: map[string]string{"zh-CN": " 艾克米 ", "en-US": ""}, SDKName: "Acme", Icon: icon}
	updated, err = service.UpdatePlatformSite(ctx, adminMeta, input)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"zh-CN": "艾克米"}, updated.Branding.Names)
	require.Equal(t, icon, updated.Branding.Icon)
	override, active := deployment.BrandOverride()
	require.Equal(t, "Acme", override.SDKName)
	require.True(t, active)
	require.Equal(t, icon, deployment.Current().Settings.Branding.Icon)
	digest := deployment.Current().IconDigest
	require.NotEmpty(t, digest)
	// 图标内容不变时刷新沿用已读取的图标，移除后摘要清空。
	require.NoError(t, deployment.Reload(ctx))
	require.Equal(t, digest, deployment.Current().IconDigest)
	input = platformSiteInputFrom(updated)
	input.Branding = appservice.PlatformBrandingSettings{Names: updated.Branding.Names, SDKName: "Acme", Icon: nil}
	_, err = service.UpdatePlatformSite(ctx, adminMeta, input)
	require.NoError(t, err)
	require.Empty(t, deployment.Current().IconDigest)
	require.Empty(t, deployment.Current().Settings.Branding.Icon)

	// 对象存储必填字段缺失返回字段错误，存储桶无法访问时不保存。
	_, err = service.UpdatePlatformStorage(ctx, adminMeta, appservice.PlatformStorageSettings{Endpoint: "relative"})
	servertest.RequireFieldError(t, err, "endpoint", i18n.FieldHTTPURLInvalid)
	servertest.RequireFieldError(t, err, "bucket", i18n.FieldStorageSettingRequired)
	_, err = service.UpdatePlatformStorage(ctx, adminMeta, appservice.PlatformStorageSettings{
		Endpoint: "http://127.0.0.1:1", PublicBaseURL: "http://127.0.0.1:1/files", Region: "us-east-1", Bucket: "files",
		AccessKeyID: "key", SecretAccessKey: "secret", ForcePathStyle: true,
	})
	servertest.RequireLocalizedError(t, err, i18n.ErrorStorageWriteFailed)
	require.Empty(t, deployment.S3().Bucket)

	// 部署配置需要平台后台会话；用户端令牌不能调用。
	member := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "部署成员", DisplayName: "成员", Email: servertest.UniqueEmail("deployment-member"), Password: "password123"})
	memberMeta := appservice.RequestMeta{Token: member.Token, WorkspaceID: member.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	_, err = service.GetPlatformDeployment(ctx, memberMeta)
	requireSessionState(t, err, appservice.SessionStateLogin)
	_, err = service.GetPlatformSite(ctx, memberMeta)
	requireSessionState(t, err, appservice.SessionStateLogin)
	_, err = service.UpdatePlatformEmail(ctx, memberMeta, appservice.PlatformEmailInput{})
	requireSessionState(t, err, appservice.SessionStateLogin)
	err = service.TestPlatformEmail(ctx, memberMeta, appservice.PlatformEmailTestInput{To: "recipient@example.test"})
	requireSessionState(t, err, appservice.SessionStateLogin)
}
