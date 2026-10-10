//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/support/random"
	"github.com/stretchr/testify/require"
)

// TestDeploymentSettings 验证部署配置：首次安装写入部署地址，平台管理员修改部署地址、部署名称、邮件发送与部署品牌后本实例立即生效、其他实例刷新后生效，
// 字段校验与服务端一致，存储桶不可访问时不保存，多台服务器运行时不能关闭对象存储，普通成员无权读取。
func TestDeploymentSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	deployment := servertest.NewDeployment(t, db)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: deployment}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	service := backend
	installed, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL + "/", WorkspaceName: "部署配置", DisplayName: "管理员", Email: servertest.UniqueEmail("deployment"),
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	adminMeta := appservice.RequestMeta{Token: installed.Auth.Token, Locale: domain.LocaleChineseSimplified}
	// 另一台服务器的部署状态在刷新前保持旧值。
	other := servertest.NewDeployment(t, db)

	// 首次安装去掉部署地址末尾的斜杠后保存，安装实例立即使用。
	current, err := service.GetPlatformDeployment(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, servertest.PublicURL, current.PublicURL)
	require.Equal(t, servertest.PublicURL, deployment.PublicURL())
	require.False(t, current.BrandingLicensed)
	require.False(t, deployment.Mailer().Enabled())

	// 部署地址必须是不带路径的完整地址；部署中没有直接提供 HTTPS 的服务器时，HTTPS 部署地址不签发证书。
	_, err = service.UpdatePlatformAddress(ctx, adminMeta, appservice.PlatformAddressInput{PublicURL: "https://support.example.test/app", Certificate: appservice.PlatformCertificate{Source: appservice.PlatformCertificateSourceACME}})
	servertest.RequireFieldError(t, err, "publicURL", i18n.FieldPublicURLInvalid)
	updated, err := service.UpdatePlatformAddress(ctx, adminMeta, appservice.PlatformAddressInput{PublicURL: "https://support.example.test/", Certificate: appservice.PlatformCertificate{Source: appservice.PlatformCertificateSourceACME}})
	require.NoError(t, err)
	require.Equal(t, "https://support.example.test", updated.PublicURL)
	require.False(t, updated.HTTPSServers)
	require.Nil(t, updated.CertificateStatus.ExpiresAt)
	// 部署名称不超过 64 个字符。
	updated, err = service.UpdatePlatformDeploymentBasics(ctx, adminMeta, appservice.PlatformDeploymentBasicsInput{Name: " 总部 ", TimeZone: current.TimeZone, TelemetryEnabled: current.TelemetryEnabled})
	require.NoError(t, err)
	require.Equal(t, "总部", updated.Name)
	require.Equal(t, "https://support.example.test", deployment.PublicURL())
	status, err := service.InstallationStatus(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified})
	require.NoError(t, err)
	require.Equal(t, "总部", status.DeploymentName)
	require.Equal(t, servertest.PublicURL, other.PublicURL(), "other instance before refresh")
	require.NoError(t, other.Reload(ctx))
	require.Equal(t, "https://support.example.test", other.PublicURL())

	// 主机非空时校验端口、加密方式、发件邮箱与成对的账号密码，保存后本实例立即可以发信。
	_, err = service.UpdatePlatformEmail(ctx, adminMeta, appservice.PlatformEmailSettings{Host: "smtp.example.test", Port: 0, Security: "ssl", Username: "mailer", FromAddress: "support"})
	servertest.RequireFieldError(t, err, "port", i18n.FieldPortInvalid)
	servertest.RequireFieldError(t, err, "security", i18n.FieldSMTPSecurityInvalid)
	servertest.RequireFieldError(t, err, "password", i18n.FieldSMTPCredentialsIncomplete)
	servertest.RequireFieldError(t, err, "fromAddress", i18n.FieldEmailInvalid)
	updated, err = service.UpdatePlatformEmail(ctx, adminMeta, appservice.PlatformEmailSettings{
		Host: " smtp.example.test ", Port: 465, Security: "TLS", Username: "mailer", Password: "secret", FromAddress: "support@example.test",
	})
	require.NoError(t, err)
	require.Equal(t, appservice.PlatformEmailSettings{Host: "smtp.example.test", Port: 465, Security: "tls", Username: "mailer", Password: "secret", FromAddress: "support@example.test"}, updated.Email)
	require.True(t, deployment.Mailer().Enabled())
	_, err = service.UpdatePlatformEmail(ctx, adminMeta, appservice.PlatformEmailSettings{Port: 587, Security: "starttls"})
	require.NoError(t, err)
	require.False(t, deployment.Mailer().Enabled())

	// 部署品牌只接受构建品牌已有语言的名称与有效的对象名和 PNG 图标；授权未授予自定义品牌时保存但不生效。
	_, err = service.UpdatePlatformBranding(ctx, adminMeta, appservice.PlatformBrandingSettings{Names: map[string]string{"fr-FR": "Acme"}, SDKName: "acme-desk", Icon: []byte("not png")})
	servertest.RequireFieldError(t, err, "names", i18n.FieldBrandNameInvalid)
	servertest.RequireFieldError(t, err, "sdkName", i18n.FieldBrandSDKNameInvalid)
	servertest.RequireFieldError(t, err, "icon", i18n.FieldBrandIconInvalid)
	icon := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)
	updated, err = service.UpdatePlatformBranding(ctx, adminMeta, appservice.PlatformBrandingSettings{Names: map[string]string{"zh-CN": " 艾克米 ", "en-US": ""}, SDKName: "Acme", Icon: icon})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"zh-CN": "艾克米"}, updated.Branding.Names)
	require.Equal(t, icon, updated.Branding.Icon)
	override, active := deployment.BrandOverride()
	require.Equal(t, "Acme", override.SDKName)
	require.False(t, active, "branding does not apply without a license")
	require.Equal(t, icon, deployment.Current().Settings.Branding.Icon)
	digest := deployment.Current().IconDigest
	require.NotEmpty(t, digest)
	// 图标内容不变时刷新沿用已读取的图标，移除后摘要清空。
	require.NoError(t, deployment.Reload(ctx))
	require.Equal(t, digest, deployment.Current().IconDigest)
	_, err = service.UpdatePlatformBranding(ctx, adminMeta, appservice.PlatformBrandingSettings{SDKName: "Acme"})
	require.NoError(t, err)
	require.Empty(t, deployment.Current().IconDigest)
	require.Empty(t, deployment.Current().Settings.Branding.Icon)

	// 开启对象存储时必填字段缺失返回字段错误，存储桶无法访问时不保存。
	_, err = service.UpdatePlatformStorage(ctx, adminMeta, appservice.PlatformStorageSettings{Enabled: true, Endpoint: "relative"})
	servertest.RequireFieldError(t, err, "endpoint", i18n.FieldHTTPURLInvalid)
	servertest.RequireFieldError(t, err, "bucket", i18n.FieldStorageSettingRequired)
	_, err = service.UpdatePlatformStorage(ctx, adminMeta, appservice.PlatformStorageSettings{
		Enabled: true, Endpoint: "http://127.0.0.1:1", PublicBaseURL: "http://127.0.0.1:1/files", Region: "us-east-1", Bucket: "files",
		AccessKeyID: "key", SecretAccessKey: "secret", ForcePathStyle: true,
	})
	servertest.RequireLocalizedError(t, err, i18n.ErrorStorageUnreachable)
	require.False(t, deployment.S3().Enabled)

	// 多台服务器运行时不能关闭对象存储，只剩一台时可以关闭。
	_, err = db.NewRaw("UPDATE platforms SET s3_enabled = true").Exec(ctx)
	require.NoError(t, err)
	admit := func() (serverinstanceaction.InstanceReport, *serverinstanceaction.InstanceLease) {
		report := serverinstanceaction.InstanceReport{ID: uuid.NewV7().String(), Hostname: "host-" + random.Hex(4), Version: buildinfo.Version, Config: serverconfig.Diagnostics{}}
		lease, err := serverinstanceaction.AdmitInstance(ctx, db, serverinstanceaction.Admission{Instance: report, Migrate: func(context.Context) error { return nil }})
		require.NoError(t, err)
		return report, lease
	}
	_, firstLease := admit()
	t.Cleanup(firstLease.Release)
	second, secondLease := admit()
	_, err = service.UpdatePlatformStorage(ctx, adminMeta, appservice.PlatformStorageSettings{})
	servertest.RequireLocalizedError(t, err, i18n.ErrorStorageRequiredByServers)
	secondLease.Release()
	require.NoError(t, serverinstanceaction.RemoveInstance(ctx, db, second.ID))
	updated, err = service.UpdatePlatformStorage(ctx, adminMeta, appservice.PlatformStorageSettings{})
	require.NoError(t, err)
	require.False(t, updated.Storage.Enabled)

	// 部署配置只对平台管理员开放。
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	require.NoError(t, err)
	owner := servertest.ResolveMemberSession(t, db, workspaces.Items[0].ID, installed.Auth.Token).Identity
	memberEmail := servertest.UniqueEmail("deployment-member")
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID})
	require.NoError(t, err)
	member := servertest.LoginMember(t, db, owner.Workspace.ID, memberEmail, "password123")
	_, err = service.GetPlatformDeployment(ctx, appservice.RequestMeta{Token: member.Token, WorkspaceID: owner.Workspace.ID, Locale: domain.LocaleChineseSimplified})
	servertest.RequireLocalizedError(t, err, i18n.ErrorPlatformAdminRequired)
}
