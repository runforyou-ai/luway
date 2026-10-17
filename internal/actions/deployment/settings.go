//go:build server

package deployment

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"strings"
	_ "time/tzdata"

	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/runforyou-ai/support/str"
	"github.com/runforyou-ai/support/validate"
	"github.com/uptrace/bun"
)

// 部署配置的字段校验码。
const (
	ValidationDeploymentNameInvalid     common.FieldCode = "PLATFORM_DEPLOYMENT_NAME_INVALID"
	ValidationTimeZoneInvalid           common.FieldCode = "PLATFORM_TIME_ZONE_INVALID"
	ValidationS3URLInvalid              common.FieldCode = "PLATFORM_S3_URL_INVALID"
	ValidationS3SettingRequired         common.FieldCode = "PLATFORM_S3_SETTING_REQUIRED"
	ValidationSMTPPortInvalid           common.FieldCode = "PLATFORM_SMTP_PORT_INVALID"
	ValidationSMTPSecurityInvalid       common.FieldCode = "PLATFORM_SMTP_SECURITY_INVALID"
	ValidationSMTPCredentialsIncomplete common.FieldCode = "PLATFORM_SMTP_CREDENTIALS_INCOMPLETE"
	ValidationSMTPFromAddressInvalid    common.FieldCode = "PLATFORM_SMTP_FROM_ADDRESS_INVALID"
	ValidationBrandNameInvalid          common.FieldCode = "PLATFORM_BRAND_NAME_INVALID"
	ValidationBrandSDKNameInvalid       common.FieldCode = "PLATFORM_BRAND_SDK_NAME_INVALID"
	ValidationBrandIconInvalid          common.FieldCode = "PLATFORM_BRAND_ICON_INVALID"
	ValidationRegistrationPolicyInvalid common.FieldCode = "PLATFORM_REGISTRATION_POLICY_INVALID"
)

const (
	// deploymentNameMaxLength 是部署名称的最大字符数。
	deploymentNameMaxLength = 64
	// brandIconMaxBytes 是网站图标的最大字节数。
	brandIconMaxBytes = 512 << 10
)

// pngSignature 是 PNG 文件开头的固定字节。
var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// DeploymentSettings 定义整个部署共用的部署配置：部署名称、部署地址与证书、平台时区、对象存储、邮件发送与部署品牌。
type DeploymentSettings struct {
	// Name 是展示给连接者的部署名称，为空时界面展示部署地址。
	Name string
	// PublicURL 是各端连接和 Web 访问使用的部署地址，也是服务端生成对外链接的根地址；平台尚未完成首次安装时为空。
	PublicURL string
	// Certificate 是直接提供 HTTPS 的服务器为 HTTPS 部署地址使用的证书。
	Certificate certificateaction.DeploymentCertificate
	// TimeZone 是划分运营数据日期的平台时区。
	TimeZone string
	// S3 是对象存储配置。
	S3 filecontent.S3Config
	// Email 是邮件接入方式、发件人与服务商参数。
	Email mail.Config
	// Branding 是部署品牌。
	Branding DeploymentBranding
}

// DeploymentBranding 定义部署品牌：按界面语言覆盖的产品名称、网站嵌入脚本对象名与网站图标，授权授予自定义品牌期间生效。
type DeploymentBranding struct {
	Names   map[string]string
	SDKName string
	// Icon 是替换 Web 端网站图标的 PNG 图片，为空表示沿用构建图标。
	Icon []byte
}

// Override 返回在构建品牌上应用的覆盖值。
func (b DeploymentBranding) Override() brand.Override {
	return brand.Override{Names: b.Names, SDKName: b.SDKName}
}

// deploymentSettingsFromModel 读取平台行中除网站图标以外的部署配置。
func deploymentSettingsFromModel(platform *servermodels.Platform) DeploymentSettings {
	return DeploymentSettings{
		Name:        platform.DeploymentName,
		PublicURL:   platform.PublicURL,
		Certificate: certificateaction.FromPlatform(platform),
		TimeZone:    platform.TimeZone,
		S3: filecontent.S3Config{
			Endpoint: platform.S3Endpoint, PublicBaseURL: platform.S3PublicBaseURL,
			Region: platform.S3Region, Bucket: platform.S3Bucket, AccessKeyID: platform.S3AccessKeyID,
			SecretAccessKey: platform.S3SecretAccessKey, ForcePathStyle: platform.S3ForcePathStyle,
		},
		Email:    platform.EmailConfig,
		Branding: DeploymentBranding{Names: platform.BrandNames, SDKName: platform.BrandSDKName},
	}
}

// DeploymentSettingsQuery 读取部署配置。
type DeploymentSettingsQuery struct {
	db *bun.DB
}

// NewDeploymentSettingsQuery 创建部署配置查询。
func NewDeploymentSettingsQuery(db *bun.DB) *DeploymentSettingsQuery {
	return &DeploymentSettingsQuery{db: db}
}

// Execute 返回数据库中当前的部署配置，包括网站图标。
func (q *DeploymentSettingsQuery) Execute(ctx context.Context) (DeploymentSettings, error) {
	platform, err := platformaction.Load(ctx, q.db)
	if err != nil {
		return DeploymentSettings{}, err
	}
	settings := deploymentSettingsFromModel(platform)
	if err := q.db.NewSelect().Model((*servermodels.Platform)(nil)).Column("brand_icon").Limit(1).Scan(ctx, &settings.Branding.Icon); err != nil {
		return DeploymentSettings{}, fmt.Errorf("load brand icon: %w", err)
	}
	return settings, nil
}

// SiteInput 定义站点设置的修改值；授权状态由事务内重新判断，未授权时不改写品牌与额度列。
type SiteInput struct {
	Name                   string
	TimeZone               string
	RegistrationPolicy     domain.RegistrationPolicy
	WorkspaceCreationQuota int
	PublicURL              string
	CertificateSource      string
	Certificate            string
	PrivateKey             string
	Branding               DeploymentBranding
}

// UpdateDeploymentAction 修改部署配置，保存后立即刷新本实例的部署状态，其他实例在下次心跳时生效。
type UpdateDeploymentAction struct {
	db           *bun.DB
	state        *DeploymentState
	enqueuer     servertask.TxEnqueuer
	certificates *certificateaction.Certificates
}

// NewUpdateDeploymentAction 创建部署配置修改操作，enqueuer 投递按新时区重建运营数据的汇总任务，certificates 为部署地址准备证书。
func NewUpdateDeploymentAction(db *bun.DB, state *DeploymentState, enqueuer servertask.TxEnqueuer, certificates *certificateaction.Certificates) *UpdateDeploymentAction {
	return &UpdateDeploymentAction{db: db, state: state, enqueuer: enqueuer, certificates: certificates}
}

// UpdateSite 一次保存站点设置：部署名称与时区、注册与创建、访问地址与证书、（已授权时）品牌；
// 仅当前已是上传证书时，空证书或私钥保留已存值；地址与证书未变时不触发签发；授权在事务内重新判断；时区变化时同事务投递汇总任务。
func (a *UpdateDeploymentAction) UpdateSite(ctx context.Context, operator *servermodels.AccountIdentity, input SiteInput) (DeploymentSettings, error) {
	name := strings.TrimSpace(input.Name)
	timeZone := strings.TrimSpace(input.TimeZone)
	quota := input.WorkspaceCreationQuota
	if quota < 0 {
		quota = 0
	}
	fields := map[string]common.FieldCode{}
	if len([]rune(name)) > deploymentNameMaxLength {
		fields["name"] = ValidationDeploymentNameInvalid
	}
	if !validate.Timezone(timeZone) {
		fields["timeZone"] = ValidationTimeZoneInvalid
	}
	if !input.RegistrationPolicy.Valid() {
		fields["registrationPolicy"] = ValidationRegistrationPolicyInvalid
	}
	publicURL, valid := certificateaction.NormalizePublicURL(input.PublicURL)
	if !valid {
		fields["publicURL"] = certificateaction.ValidationPublicURLInvalid
	}
	if len(fields) > 0 {
		return DeploymentSettings{}, &common.FieldError{Fields: fields}
	}

	https := strings.HasPrefix(publicURL, "https://")
	if https {
		switch input.CertificateSource {
		case string(domain.CertificateSourceACME), string(domain.CertificateSourceUpload):
		default:
			return DeploymentSettings{}, &common.FieldError{Fields: map[string]common.FieldCode{
				"certificateSource": certificateaction.ValidationCertificateSourceInvalid,
			}}
		}
	}

	var settings DeploymentSettings
	// 全程持有证书锁，并在锁内重读后再决定是否 Prepare，避免并发改地址/证书与「跳过签发」交错。
	err := a.certificates.WithLock(ctx, func(ctx context.Context) error {
		current, err := platformaction.Load(ctx, a.db)
		if err != nil {
			return err
		}
		chain, privateKey := strings.TrimSpace(input.Certificate), strings.TrimSpace(input.PrivateKey)
		// 仅当前已是上传证书时，空值保留已存 PEM，避免把 ACME 证书静默改成上传来源。
		if input.CertificateSource == string(domain.CertificateSourceUpload) && current.CertificateSource == string(domain.CertificateSourceUpload) {
			if chain == "" {
				chain = current.Certificate
			}
			if privateKey == "" {
				privateKey = current.CertificatePrivateKey
			}
		}
		// 地址与证书未变时跳过 Prepare：上传看材料（原样回传亦视为未改）；ACME 仅在已有覆盖该主机名的证书时跳过，
		// 以便无 HTTPS 服务器时先保存地址、服务器上线后再保存仍能首次签发；过期续期仍交给后台任务，避免阻塞无关设置。
		certificateUnchanged := false
		if publicURL == current.PublicURL && (!https || input.CertificateSource == current.CertificateSource) {
			switch {
			case !https:
				certificateUnchanged = true
			case input.CertificateSource == string(domain.CertificateSourceUpload):
				certificateUnchanged = chain == current.Certificate && privateKey == current.CertificatePrivateKey
			case input.CertificateSource == string(domain.CertificateSourceACME):
				host := ""
				if parsed, err := url.Parse(publicURL); err == nil {
					host = parsed.Hostname()
				}
				if host != "" && current.CertificateSource == string(domain.CertificateSourceACME) {
					if cert, err := certificateaction.ParseCertificate(current.Certificate, current.CertificatePrivateKey); err == nil && cert.Leaf.VerifyHostname(host) == nil {
						certificateUnchanged = true
					}
				}
			}
		}

		var change *certificateaction.CertificateChange
		if !certificateUnchanged {
			var certFields map[string]common.FieldCode
			change, certFields, err = a.certificates.Prepare(ctx, current, publicURL, input.CertificateSource, chain, privateKey)
			if err != nil {
				return err
			}
			if len(certFields) > 0 {
				return &common.FieldError{Fields: certFields}
			}
		}

		settings, err = a.update(ctx, operator, "site", func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
			// 锁外到落库之间若平台行已被改写地址或证书，拒绝本次跳过签发的保存，避免写回旧域名却保留新证书。
			if change == nil && (platform.PublicURL != current.PublicURL ||
				platform.CertificateSource != current.CertificateSource ||
				platform.Certificate != current.Certificate ||
				platform.CertificatePrivateKey != current.CertificatePrivateKey) {
				return certificateaction.ErrCertificateBusy
			}
			licensed, err := platformaction.Licensed(ctx, tx)
			if err != nil {
				return err
			}
			timeZoneChanged := platform.TimeZone != timeZone
			platform.DeploymentName, platform.TimeZone = name, timeZone
			platform.StatisticsRebuildPending = platform.StatisticsRebuildPending || timeZoneChanged
			platform.RegistrationPolicy = string(input.RegistrationPolicy)
			platform.PublicURL = publicURL
			columns := []string{"deployment_name", "time_zone", "statistics_rebuild_pending", "registration_policy", "public_url"}
			var branding DeploymentBranding
			if licensed {
				var brandFields map[string]common.FieldCode
				branding, brandFields = normalizeBranding(input.Branding)
				if len(brandFields) > 0 {
					return &common.FieldError{Fields: brandFields}
				}
				if quota < platform.WorkspaceCreationQuota {
					return &common.FieldError{Fields: map[string]common.FieldCode{
						"workspaceCreationQuota": platformaction.ValidationWorkspaceCreationQuotaDecrease,
					}}
				}
				platform.WorkspaceCreationQuota = quota
				columns = append(columns, "workspace_creation_quota")
				platform.BrandNames, platform.BrandSDKName = maps.Clone(branding.Names), branding.SDKName
				columns = append(columns, "brand_names", "brand_sdk_name")
			}
			// 证书来源只随 HTTPS 部署地址保存，HTTP 部署地址沿用已保存的来源与证书。
			if https {
				platform.CertificateSource = input.CertificateSource
				columns = append(columns, "certificate_source")
			}
			q := tx.NewUpdate().Model(platform).Column(columns...)
			if licensed {
				q = q.Set("brand_icon = ?", branding.Icon)
			}
			if _, err := q.WherePK().Exec(ctx); err != nil {
				return err
			}
			if change != nil {
				if err := change.Apply(ctx, tx, platform); err != nil {
					return err
				}
			}
			if !timeZoneChanged {
				return nil
			}
			_, err = a.enqueuer.EnqueueIn(ctx, platformaction.AggregateStatsActionName, platformaction.AggregateStatsInput{}, servertask.EnqueueOptions{
				Queue: platformaction.StatsQueue, MaxAttempts: 3,
			})
			return err
		})
		return err
	})
	return settings, err
}

// normalizeBranding 校验并规范化部署品牌；空图标表示清除。
func normalizeBranding(input DeploymentBranding) (DeploymentBranding, map[string]common.FieldCode) {
	names := map[string]string{}
	fields := map[string]common.FieldCode{}
	for locale, name := range input.Names {
		name = strings.TrimSpace(name)
		if _, ok := brand.Build().Names[locale]; !ok {
			fields["names"] = ValidationBrandNameInvalid
			continue
		}
		if name != "" {
			names[locale] = name
		}
	}
	branding := DeploymentBranding{Names: names, SDKName: strings.TrimSpace(input.SDKName), Icon: input.Icon}
	if brand.Build().WithOverride(brand.Override{Names: branding.Names}).Validate() != nil {
		fields["names"] = ValidationBrandNameInvalid
	}
	if brand.Build().WithOverride(brand.Override{SDKName: branding.SDKName}).Validate() != nil {
		fields["sdkName"] = ValidationBrandSDKNameInvalid
	}
	if len(branding.Icon) > 0 && (len(branding.Icon) > brandIconMaxBytes || !bytes.HasPrefix(branding.Icon, pngSignature)) {
		fields["icon"] = ValidationBrandIconInvalid
	}
	if len(branding.Icon) == 0 {
		branding.Icon = nil
	}
	return branding, fields
}

// UpdateStorage 完整验证 S3 读写与访问地址后保存配置，返回源站匿名读取结果。
func (a *UpdateDeploymentAction) UpdateStorage(ctx context.Context, operator *servermodels.AccountIdentity, input filecontent.S3Config) (domain.StorageOriginAccess, error) {
	config := filecontent.S3Config{
		Endpoint:      strings.TrimRight(strings.TrimSpace(input.Endpoint), "/"),
		PublicBaseURL: strings.TrimRight(strings.TrimSpace(input.PublicBaseURL), "/"), Region: strings.TrimSpace(input.Region),
		Bucket: strings.TrimSpace(input.Bucket), AccessKeyID: strings.TrimSpace(input.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(input.SecretAccessKey), ForcePathStyle: input.ForcePathStyle,
	}
	fields := map[string]common.FieldCode{}
	for field, value := range map[string]string{"endpoint": config.Endpoint, "publicBaseURL": config.PublicBaseURL} {
		if !str.IsHTTPBaseURL(value) {
			fields[field] = ValidationS3URLInvalid
		}
	}
	for field, value := range map[string]string{"region": config.Region, "bucket": config.Bucket, "accessKeyID": config.AccessKeyID, "secretAccessKey": config.SecretAccessKey} {
		if value == "" {
			fields[field] = ValidationS3SettingRequired
		}
	}
	if len(fields) > 0 {
		return "", &common.FieldError{Fields: fields}
	}
	access, err := filecontent.CheckStorage(ctx, config)
	if err != nil {
		return "", err
	}
	_, err = a.update(ctx, operator, "storage", func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		platform.S3Endpoint, platform.S3PublicBaseURL = config.Endpoint, config.PublicBaseURL
		platform.S3Region, platform.S3Bucket, platform.S3ForcePathStyle = config.Region, config.Bucket, config.ForcePathStyle
		platform.S3AccessKeyID, platform.S3SecretAccessKey = config.AccessKeyID, config.SecretAccessKey
		_, err := tx.NewUpdate().Model(platform).Column("s3_endpoint", "s3_public_base_url", "s3_region", "s3_bucket", "s3_access_key_id", "s3_secret_access_key", "s3_force_path_style").WherePK().Exec(ctx)
		return err
	})
	return access, err
}

// UpdateEmail 在平台行事务锁内校验并保存当前邮件服务商配置。
func (a *UpdateDeploymentAction) UpdateEmail(ctx context.Context, operator *servermodels.AccountIdentity, input mail.Config) (DeploymentSettings, error) {
	return a.update(ctx, operator, "email", func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		config, fields := normalizeEmail(input, platform.EmailConfig)
		if len(fields) > 0 {
			return &common.FieldError{Fields: fields}
		}
		platform.EmailConfig = config
		_, err := tx.NewUpdate().Model(platform).Column("email_config").WherePK().Exec(ctx)
		return err
	})
}

// update 在事务内确认操作者仍是有效平台管理员并锁定平台行，执行 section 分组的修改后刷新本实例的部署状态并返回最新的部署配置；刷新失败时由下次心跳刷新。
func (a *UpdateDeploymentAction) update(ctx context.Context, operator *servermodels.AccountIdentity, section string, update func(context.Context, bun.Tx, *servermodels.Platform) error) (DeploymentSettings, error) {
	if _, err := platformaction.Update(ctx, a.db, operator, update); err != nil {
		return DeploymentSettings{}, err
	}
	slog.InfoContext(logscope.WithAccount(ctx, operator.Account.ID), "已保存部署配置", "section", section)
	if err := a.state.Reload(context.WithoutCancel(ctx)); err != nil {
		slog.WarnContext(ctx, "保存部署配置后刷新部署状态失败", "error", err)
	}
	return NewDeploymentSettingsQuery(a.db).Execute(ctx)
}

// HTTPSServersOnline 判断部署中是否有心跳在线且直接提供 HTTPS 的服务器。
func (q *DeploymentSettingsQuery) HTTPSServersOnline(ctx context.Context) (bool, error) {
	return serverinstanceaction.HTTPSServersOnline(ctx, q.db)
}
