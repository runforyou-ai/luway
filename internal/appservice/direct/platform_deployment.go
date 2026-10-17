//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"maps"

	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/mapx"
)

// GetPlatformSite 返回站点设置；未授权时品牌为构建默认值、额度为 0。
func (o *platformOps) GetPlatformSite(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformSite, error) {
	settings, err := o.deploymentRead.Execute(ctx)
	if err != nil {
		return appservice.PlatformSite{}, platformError(meta, err, i18n.ErrorPlatformSettingsReadFailed)
	}
	policies, err := o.platformSettingsRead.Execute(ctx)
	if err != nil {
		return appservice.PlatformSite{}, platformError(meta, err, i18n.ErrorPlatformSettingsReadFailed)
	}
	return o.platformSite(ctx, meta, settings, policies.RegistrationPolicy, policies.WorkspaceCreationQuota)
}

// UpdatePlatformSite 一次保存站点设置。
func (o *platformOps) UpdatePlatformSite(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformSiteInput) (appservice.PlatformSite, error) {
	settings, err := o.updateDeployment.UpdateSite(ctx, account, deploymentaction.SiteInput{
		Name:                   input.Name,
		TimeZone:               input.TimeZone,
		RegistrationPolicy:     input.RegistrationPolicy,
		WorkspaceCreationQuota: input.WorkspaceCreationQuota,
		PublicURL:              input.PublicURL,
		CertificateSource:      string(input.Certificate.Source),
		Certificate:            input.Certificate.Certificate,
		PrivateKey:             input.Certificate.PrivateKey,
		Branding: deploymentaction.DeploymentBranding{
			Names: input.Branding.Names, SDKName: input.Branding.SDKName, Icon: input.Branding.Icon,
		},
	})
	if issueErr, ok := certificateIssueError(ctx, meta, err); ok {
		return appservice.PlatformSite{}, issueErr
	}
	if err != nil {
		return appservice.PlatformSite{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	policies, err := o.platformSettingsRead.Execute(ctx)
	if err != nil {
		return appservice.PlatformSite{}, platformError(meta, err, i18n.ErrorPlatformSettingsReadFailed)
	}
	slog.InfoContext(ctx, "站点设置已修改", "public_url", settings.PublicURL, "time_zone", settings.TimeZone,
		"registration_policy", policies.RegistrationPolicy, "workspace_creation_quota", policies.WorkspaceCreationQuota)
	return o.platformSite(ctx, meta, settings, policies.RegistrationPolicy, policies.WorkspaceCreationQuota)
}

// GetPlatformDeployment 返回对象存储与邮件发送配置。
func (o *platformOps) GetPlatformDeployment(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformDeployment, error) {
	settings, err := o.deploymentRead.Execute(ctx)
	if err != nil {
		return appservice.PlatformDeployment{}, platformError(meta, err, i18n.ErrorPlatformSettingsReadFailed)
	}
	return appservice.PlatformDeployment{
		Storage: appservice.PlatformStorageSettings(settings.S3),
		Email:   emailSettingsFromConfig(settings.Email),
	}, nil
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

// UpdatePlatformStorage 校验真实读写和访问地址后保存对象存储配置。
func (o *platformOps) UpdatePlatformStorage(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformStorageSettings) (appservice.PlatformStorageCheck, error) {
	access, err := o.updateDeployment.UpdateStorage(ctx, account, serverfilecontent.S3Config(input))
	if check, ok := errors.AsType[*serverfilecontent.CheckError](err); ok {
		slog.WarnContext(ctx, "对象存储连接测试失败", "stage", check.Stage, "error", err)
		key := i18n.ErrorStorageUnreachable
		switch check.Stage {
		case "write":
			key = i18n.ErrorStorageWriteFailed
		case "read":
			key = i18n.ErrorStorageReadFailed
		case "public":
			key = i18n.ErrorStoragePublicReadFailed
		case "delete":
			key = i18n.ErrorStorageDeleteFailed
		}
		return appservice.PlatformStorageCheck{}, appservice.InvalidError(meta, key, nil)
	}
	if err != nil {
		return appservice.PlatformStorageCheck{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "对象存储配置已修改", "origin_access", access)
	return appservice.PlatformStorageCheck{OriginAccess: access}, nil
}

// UpdatePlatformEmail 修改当前邮件接入方式与发件人配置。
func (o *platformOps) UpdatePlatformEmail(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformEmailInput) (appservice.PlatformDeployment, error) {
	settings, err := o.updateDeployment.UpdateEmail(ctx, account, emailConfigFromInput(input))
	if err != nil {
		return appservice.PlatformDeployment{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "邮件发送配置已修改", "enabled", settings.Email.Enabled)
	return appservice.PlatformDeployment{
		Storage: appservice.PlatformStorageSettings(settings.S3),
		Email:   emailSettingsFromConfig(settings.Email),
	}, nil
}

// platformSite 把部署配置、策略与授权能力转换为站点设置契约；未授权时品牌填构建默认值。
func (o *platformOps) platformSite(ctx context.Context, meta appservice.RequestMeta, settings deploymentaction.DeploymentSettings, registrationPolicy appservice.RegistrationPolicy, workspaceCreationQuota int) (appservice.PlatformSite, error) {
	httpsServers, err := o.deploymentRead.HTTPSServersOnline(ctx)
	if err != nil {
		return appservice.PlatformSite{}, appservice.FailedError(meta, i18n.ErrorPlatformSettingsReadFailed, err)
	}
	license, err := o.licenseRead.Execute(ctx)
	if err != nil {
		return appservice.PlatformSite{}, platformError(meta, err, i18n.ErrorPlatformSettingsReadFailed)
	}
	licensed := license.Licensed()
	certificate := appservice.PlatformCertificate{Source: appservice.PlatformCertificateSource(settings.Certificate.Source)}
	if certificate.Source == appservice.PlatformCertificateSourceUpload {
		certificate.Certificate, certificate.PrivateKey = settings.Certificate.Chain, settings.Certificate.PrivateKey
	}
	branding := appservice.PlatformBrandingSettings{
		Names: mapx.OrEmpty(settings.Branding.Names), SDKName: settings.Branding.SDKName, Icon: settings.Branding.Icon,
	}
	if !licensed {
		built := brand.Build()
		branding = appservice.PlatformBrandingSettings{
			Names: maps.Clone(built.Names), SDKName: built.SDKName, Icon: nil,
		}
	}
	return appservice.PlatformSite{
		Name: settings.Name, TimeZone: settings.TimeZone,
		RegistrationPolicy: registrationPolicy, WorkspaceCreationQuota: workspaceCreationQuota,
		PublicURL: settings.PublicURL, Certificate: certificate, CertificateStatus: platformCertificateStatus(settings.Certificate),
		HTTPSServers: httpsServers, Branding: branding, BrandingLicensed: licensed,
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
			Endpoint: settings.S3.Endpoint, PublicBaseURL: settings.S3.PublicBaseURL,
			Region: settings.S3.Region, Bucket: settings.S3.Bucket, ForcePathStyle: settings.S3.ForcePathStyle,
		},
		Email: appservice.PlatformEmailSummary{
			Enabled: settings.Email.Enabled, Provider: appservice.EmailProvider(settings.Email.Provider), FromAddress: settings.Email.FromAddress, FromName: settings.Email.FromName,
		},
		Branding: appservice.PlatformBrandingSummary{Names: mapx.OrEmpty(settings.Branding.Names), SDKName: settings.Branding.SDKName, HasIcon: len(settings.Branding.Icon) > 0},
	}
}
