//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/runforyou-ai/support/mapx"
)

// GetPlatformDeployment 返回部署名称、部署地址与证书、平台时区、上报开关、对象存储、邮件发送与部署品牌。
func (o *platformOps) GetPlatformDeployment(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformDeployment, error) {
	settings, err := o.deploymentRead.Execute(ctx)
	if err != nil {
		return appservice.PlatformDeployment{}, platformError(meta, err, i18n.ErrorPlatformSettingsReadFailed)
	}
	return o.platformDeployment(ctx, meta, settings)
}

// UpdatePlatformDeploymentBasics 修改部署名称、平台时区与上报开关，时区变化时在后台按新时区重建运营数据。
func (o *platformOps) UpdatePlatformDeploymentBasics(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformDeploymentBasicsInput) (appservice.PlatformDeployment, error) {
	settings, err := o.updateDeployment.UpdateBasics(ctx, account, deploymentaction.DeploymentBasics(input))
	if err != nil {
		return appservice.PlatformDeployment{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "部署基本配置已修改", "time_zone", settings.TimeZone, "telemetry_enabled", settings.TelemetryEnabled)
	return o.platformDeployment(ctx, meta, settings)
}

// UpdatePlatformAddress 修改部署地址与证书，自动签发失败时返回带失败原因的错误。
func (o *platformOps) UpdatePlatformAddress(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformAddressInput) (appservice.PlatformDeployment, error) {
	settings, err := o.updateDeployment.UpdateAddress(ctx, account, deploymentaction.DeploymentAddress{
		PublicURL: input.PublicURL, CertificateSource: string(input.Certificate.Source),
		Certificate: input.Certificate.Certificate, PrivateKey: input.Certificate.PrivateKey,
	})
	if issueErr, ok := certificateIssueError(ctx, meta, err); ok {
		return appservice.PlatformDeployment{}, issueErr
	}
	if err != nil {
		return appservice.PlatformDeployment{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "部署地址已修改", "public_url", settings.PublicURL, "certificate_source", settings.Certificate.Source)
	return o.platformDeployment(ctx, meta, settings)
}

// certificateIssueError 把正在签发、签发超时与自动签发失败转换为业务错误，签发失败的文案带失败原因；其他错误时 ok 为假。
func certificateIssueError(ctx context.Context, meta appservice.RequestMeta, err error) (error, bool) {
	if errors.Is(err, certificateaction.ErrCertificateBusy) {
		return appservice.ConflictError(meta, i18n.ErrorCertificateBusy, "certificate_busy"), true
	}
	issueErr, ok := errors.AsType[*certificateaction.CertificateIssueError](err)
	if !ok {
		return nil, false
	}
	if errors.Is(issueErr, context.DeadlineExceeded) {
		slog.WarnContext(ctx, "签发部署地址证书超时", "domain", issueErr.Domain)
		return appservice.InvalidError(meta, i18n.ErrorCertificateIssueTimeout, nil), true
	}
	slog.WarnContext(ctx, "签发部署地址证书失败", "domain", issueErr.Domain, "error", issueErr.Err)
	return appservice.InvalidTemplateError(meta, i18n.ErrorCertificateIssueFailed, map[string]any{"Reason": issueErr.Reason}), true
}

// UpdatePlatformStorage 修改对象存储配置：开启时先确认能访问存储桶，部署中有其他服务器运行时不能关闭。
func (o *platformOps) UpdatePlatformStorage(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformStorageSettings) (appservice.PlatformDeployment, error) {
	settings, err := o.updateDeployment.UpdateStorage(ctx, account, serverfilecontent.S3Config(input))
	if errors.Is(err, deploymentaction.ErrStorageUnreachable) {
		slog.WarnContext(ctx, "对象存储不可访问", "error", err)
		return appservice.PlatformDeployment{}, appservice.InvalidError(meta, i18n.ErrorStorageUnreachable, nil)
	}
	if errors.Is(err, deploymentaction.ErrStorageRequiredByServers) {
		return appservice.PlatformDeployment{}, appservice.InvalidError(meta, i18n.ErrorStorageRequiredByServers, nil)
	}
	if err != nil {
		return appservice.PlatformDeployment{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "对象存储配置已修改", "enabled", settings.S3.Enabled)
	return o.platformDeployment(ctx, meta, settings)
}

// UpdatePlatformEmail 修改 SMTP 邮件发送配置，主机为空时关闭邮件发送。
func (o *platformOps) UpdatePlatformEmail(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformEmailSettings) (appservice.PlatformDeployment, error) {
	settings, err := o.updateDeployment.UpdateEmail(ctx, account, mail.Config(input))
	if err != nil {
		return appservice.PlatformDeployment{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "邮件发送配置已修改", "enabled", settings.SMTP.Host != "")
	return o.platformDeployment(ctx, meta, settings)
}

// UpdatePlatformBranding 修改部署品牌，授权授予自定义品牌期间生效。
func (o *platformOps) UpdatePlatformBranding(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformBrandingSettings) (appservice.PlatformDeployment, error) {
	settings, err := o.updateDeployment.UpdateBranding(ctx, account, deploymentaction.DeploymentBranding(input))
	if err != nil {
		return appservice.PlatformDeployment{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "部署品牌已修改")
	return o.platformDeployment(ctx, meta, settings)
}

// UpdatePlatformHome 修改产品首页配置，首页提供价格区块时生效。
func (o *platformOps) UpdatePlatformHome(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformHomeSettings) (appservice.PlatformDeployment, error) {
	settings, err := o.updateDeployment.UpdateHome(ctx, account, input.SelfHost)
	if err != nil {
		return appservice.PlatformDeployment{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "产品首页配置已修改", "self_host", settings.HomeSelfHost)
	return o.platformDeployment(ctx, meta, settings)
}

// platformDeployment 把部署配置与授权当前授予的能力转换为应用契约。
func (o *platformOps) platformDeployment(ctx context.Context, meta appservice.RequestMeta, settings deploymentaction.DeploymentSettings) (appservice.PlatformDeployment, error) {
	capabilities, err := o.deploymentRead.Capabilities(ctx)
	if err != nil {
		return appservice.PlatformDeployment{}, appservice.FailedError(meta, i18n.ErrorPlatformSettingsReadFailed, err)
	}
	httpsServers, err := o.deploymentRead.HTTPSServersOnline(ctx)
	if err != nil {
		return appservice.PlatformDeployment{}, appservice.FailedError(meta, i18n.ErrorPlatformSettingsReadFailed, err)
	}
	certificate := appservice.PlatformCertificate{Source: appservice.PlatformCertificateSource(settings.Certificate.Source)}
	if certificate.Source == appservice.PlatformCertificateSourceUpload {
		certificate.Certificate, certificate.PrivateKey = settings.Certificate.Chain, settings.Certificate.PrivateKey
	}
	return appservice.PlatformDeployment{
		Name: settings.Name, PublicURL: settings.PublicURL, TimeZone: settings.TimeZone, TelemetryEnabled: settings.TelemetryEnabled,
		Certificate: certificate, CertificateStatus: platformCertificateStatus(settings.Certificate), HTTPSServers: httpsServers,
		Storage:          appservice.PlatformStorageSettings(settings.S3),
		Email:            appservice.PlatformEmailSettings(settings.SMTP),
		Branding:         appservice.PlatformBrandingSettings{Names: mapx.OrEmpty(settings.Branding.Names), SDKName: settings.Branding.SDKName, Icon: settings.Branding.Icon},
		Home:             appservice.PlatformHomeSettings{SelfHost: settings.HomeSelfHost},
		BrandingLicensed: capabilities.CustomBranding,
		HomePricing:      o.homePricing,
	}, nil
}

// platformCertificateStatus 返回部署地址证书包含的域名、到期时间与最近一次自动签发失败记录。
func platformCertificateStatus(certificate certificateaction.DeploymentCertificate) appservice.PlatformCertificateStatus {
	status := appservice.PlatformCertificateStatus{
		Domains: []string{}, ExpiresAt: certificate.ExpiresAt,
		RenewalError: certificate.RenewalError, RenewalFailedAt: certificate.RenewalFailedAt,
	}
	if parsed, err := certificateaction.ParseCertificate(certificate.Chain, certificate.PrivateKey); err == nil {
		status.Domains = parsed.Leaf.DNSNames
	}
	return status
}

// platformDeploymentSummaryFromAction 把部署配置转换为不含密码与密钥的诊断信息。
func platformDeploymentSummaryFromAction(settings deploymentaction.DeploymentSettings) appservice.PlatformDeploymentSummary {
	return appservice.PlatformDeploymentSummary{
		Name: settings.Name, PublicURL: settings.PublicURL,
		CertificateSource: appservice.PlatformCertificateSource(settings.Certificate.Source), CertificateStatus: platformCertificateStatus(settings.Certificate),
		Storage: appservice.PlatformStorageSummary{
			Enabled: settings.S3.Enabled, Endpoint: settings.S3.Endpoint, PublicBaseURL: settings.S3.PublicBaseURL,
			Region: settings.S3.Region, Bucket: settings.S3.Bucket, ForcePathStyle: settings.S3.ForcePathStyle,
		},
		Email: appservice.PlatformEmailSummary{
			Enabled: settings.SMTP.Host != "", Host: settings.SMTP.Host, Port: settings.SMTP.Port,
			Security: settings.SMTP.Security, FromAddress: settings.SMTP.FromAddress,
		},
		Branding: appservice.PlatformBrandingSummary{Names: mapx.OrEmpty(settings.Branding.Names), SDKName: settings.Branding.SDKName, HasIcon: len(settings.Branding.Icon) > 0},
		Home:     appservice.PlatformHomeSettings{SelfHost: settings.HomeSelfHost},
	}
}
