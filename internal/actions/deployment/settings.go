//go:build server

package deployment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"
	_ "time/tzdata"

	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
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
)

var (
	// ErrStorageUnreachable 表示无法用填写的对象存储配置访问存储桶。
	ErrStorageUnreachable = errors.New("object storage bucket is unreachable")
	// ErrStorageRequiredByServers 表示部署中有多台服务器运行，不能关闭对象存储。
	ErrStorageRequiredByServers = errors.New("object storage is required while several servers are running")
)

const (
	// deploymentNameMaxLength 是部署名称的最大字符数。
	deploymentNameMaxLength = 64
	// brandIconMaxBytes 是网站图标的最大字节数。
	brandIconMaxBytes = 512 << 10
	// bucketCheckTimeout 是检查对象存储桶的时限。
	bucketCheckTimeout = 3 * time.Second
)

// pngSignature 是 PNG 文件开头的固定字节。
var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// DeploymentSettings 定义整个部署共用的部署配置：部署名称、部署地址与证书、平台时区、上报开关、对象存储、邮件发送与部署品牌。
type DeploymentSettings struct {
	// Name 是展示给连接者的部署名称，为空时界面展示部署地址。
	Name string
	// PublicURL 是各端连接和 Web 访问使用的部署地址，也是服务端生成对外链接的根地址；平台尚未完成首次安装时为空。
	PublicURL string
	// Certificate 是直接提供 HTTPS 的服务器为 HTTPS 部署地址使用的证书。
	Certificate certificateaction.DeploymentCertificate
	// TimeZone 是划分运营数据日期的平台时区。
	TimeZone string
	// TelemetryEnabled 表示是否向 control 上报运行指标与错误。
	TelemetryEnabled bool
	// S3 是对象存储配置。
	S3 filecontent.S3Config
	// SMTP 是邮件发送配置，主机为空时关闭邮件发送。
	SMTP mail.Config
	// Branding 是部署品牌。
	Branding DeploymentBranding
	// HomeSelfHost 表示首页提供价格区块时展示自部署介绍；不提供价格区块时首页始终展示。
	HomeSelfHost bool
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
		Name:             platform.DeploymentName,
		PublicURL:        platform.PublicURL,
		Certificate:      certificateaction.FromPlatform(platform),
		TimeZone:         platform.TimeZone,
		TelemetryEnabled: platform.TelemetryEnabled,
		S3: filecontent.S3Config{
			Enabled: platform.S3Enabled, Endpoint: platform.S3Endpoint, PublicBaseURL: platform.S3PublicBaseURL,
			Region: platform.S3Region, Bucket: platform.S3Bucket, AccessKeyID: platform.S3AccessKeyID,
			SecretAccessKey: platform.S3SecretAccessKey, ForcePathStyle: platform.S3ForcePathStyle,
		},
		SMTP: mail.Config{
			Host: platform.SMTPHost, Port: platform.SMTPPort, Username: platform.SMTPUsername, Password: platform.SMTPPassword,
			Security: platform.SMTPSecurity, FromAddress: platform.SMTPFromAddress,
		},
		Branding:     DeploymentBranding{Names: platform.BrandNames, SDKName: platform.BrandSDKName},
		HomeSelfHost: platform.HomeSelfHost,
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

// DeploymentBasics 定义部署名称、平台时区与上报开关。
type DeploymentBasics struct {
	Name             string
	TimeZone         string
	TelemetryEnabled bool
}

// DeploymentAddress 定义部署地址与证书来源；证书来源为上传时 Certificate 与 PrivateKey 是 PEM 证书链与私钥。
type DeploymentAddress struct {
	PublicURL         string
	CertificateSource string
	Certificate       string
	PrivateKey        string
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

// UpdateAddress 校验后由仍有效的平台管理员保存部署地址与证书，HTTP 部署地址不修改证书来源与证书；上传的证书须与私钥匹配、未到期且包含 HTTPS 部署地址的主机名；自动签发时，HTTPS 部署地址缺少有效证书且部署中有直接提供 HTTPS 的服务器时先签发证书，签发成功后与部署地址一起保存。
func (a *UpdateDeploymentAction) UpdateAddress(ctx context.Context, operator *servermodels.AccountIdentity, input DeploymentAddress) (DeploymentSettings, error) {
	publicURL, valid := certificateaction.NormalizePublicURL(input.PublicURL)
	if !valid {
		return DeploymentSettings{}, &common.FieldError{Fields: map[string]common.FieldCode{"publicURL": certificateaction.ValidationPublicURLInvalid}}
	}
	var settings DeploymentSettings
	err := a.certificates.WithLock(ctx, func(ctx context.Context) error {
		current, err := platformaction.Load(ctx, a.db)
		if err != nil {
			return err
		}
		change, fields, err := a.certificates.Prepare(ctx, current, publicURL, input.CertificateSource, strings.TrimSpace(input.Certificate), strings.TrimSpace(input.PrivateKey))
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			return &common.FieldError{Fields: fields}
		}
		settings, err = a.update(ctx, operator, "address", func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
			platform.PublicURL = publicURL
			columns := []string{"public_url"}
			// 证书来源只随 HTTPS 部署地址保存，HTTP 部署地址沿用已保存的来源与证书。
			if strings.HasPrefix(publicURL, "https://") {
				platform.CertificateSource = input.CertificateSource
				columns = append(columns, "certificate_source")
			}
			if _, err := tx.NewUpdate().Model(platform).Column(columns...).WherePK().Exec(ctx); err != nil {
				return err
			}
			if change == nil {
				return nil
			}
			return change.Apply(ctx, tx, platform)
		})
		return err
	})
	return settings, err
}

// UpdateBasics 校验后由仍有效的平台管理员保存部署名称、平台时区与上报开关；时区变化时标记重建并在同一事务内投递汇总任务，立即按新时区从安装日起重建运营数据，任务失败时由定时汇总继续重建。
func (a *UpdateDeploymentAction) UpdateBasics(ctx context.Context, operator *servermodels.AccountIdentity, input DeploymentBasics) (DeploymentSettings, error) {
	basics := DeploymentBasics{Name: strings.TrimSpace(input.Name), TimeZone: strings.TrimSpace(input.TimeZone), TelemetryEnabled: input.TelemetryEnabled}
	fields := map[string]common.FieldCode{}
	if len([]rune(basics.Name)) > deploymentNameMaxLength {
		fields["name"] = ValidationDeploymentNameInvalid
	}
	if !validate.Timezone(basics.TimeZone) {
		fields["timeZone"] = ValidationTimeZoneInvalid
	}
	if len(fields) > 0 {
		return DeploymentSettings{}, &common.FieldError{Fields: fields}
	}
	return a.update(ctx, operator, "basics", func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		timeZoneChanged := platform.TimeZone != basics.TimeZone
		platform.DeploymentName, platform.TimeZone, platform.TelemetryEnabled = basics.Name, basics.TimeZone, basics.TelemetryEnabled
		platform.StatisticsRebuildPending = platform.StatisticsRebuildPending || timeZoneChanged
		if _, err := tx.NewUpdate().Model(platform).
			Column("deployment_name", "time_zone", "telemetry_enabled", "statistics_rebuild_pending").WherePK().Exec(ctx); err != nil {
			return err
		}
		if !timeZoneChanged {
			return nil
		}
		_, err := a.enqueuer.EnqueueIn(ctx, platformaction.AggregateStatsActionName, platformaction.AggregateStatsInput{}, servertask.EnqueueOptions{
			Queue: platformaction.StatsQueue, MaxAttempts: 3,
		})
		return err
	})
}

// UpdateStorage 校验后由仍有效的平台管理员保存对象存储配置：开启时先确认能用该配置访问存储桶，部署中有其他服务器运行时不能关闭。
func (a *UpdateDeploymentAction) UpdateStorage(ctx context.Context, operator *servermodels.AccountIdentity, input filecontent.S3Config) (DeploymentSettings, error) {
	config := filecontent.S3Config{
		Enabled: input.Enabled, Endpoint: strings.TrimRight(strings.TrimSpace(input.Endpoint), "/"),
		PublicBaseURL: strings.TrimRight(strings.TrimSpace(input.PublicBaseURL), "/"), Region: strings.TrimSpace(input.Region),
		Bucket: strings.TrimSpace(input.Bucket), AccessKeyID: strings.TrimSpace(input.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(input.SecretAccessKey), ForcePathStyle: input.ForcePathStyle,
	}
	if config.Enabled {
		fields := map[string]common.FieldCode{}
		if !str.IsHTTPBaseURL(config.Endpoint) {
			fields["endpoint"] = ValidationS3URLInvalid
		}
		if !str.IsHTTPBaseURL(config.PublicBaseURL) {
			fields["publicBaseURL"] = ValidationS3URLInvalid
		}
		// 区域、存储桶与访问密钥在开启时必填。
		for field, value := range map[string]string{"region": config.Region, "bucket": config.Bucket, "accessKeyID": config.AccessKeyID, "secretAccessKey": config.SecretAccessKey} {
			if value == "" {
				fields[field] = ValidationS3SettingRequired
			}
		}
		if len(fields) > 0 {
			return DeploymentSettings{}, &common.FieldError{Fields: fields}
		}
		checkCtx, cancel := context.WithTimeout(ctx, bucketCheckTimeout)
		err := filecontent.CheckBucket(checkCtx, config)
		cancel()
		if err != nil {
			return DeploymentSettings{}, fmt.Errorf("%w: %w", ErrStorageUnreachable, err)
		}
	}
	// save 在事务内写入对象存储配置并返回最新的部署配置。
	save := func(ctx context.Context) (DeploymentSettings, error) {
		return a.update(ctx, operator, "storage", func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
			platform.S3Enabled, platform.S3Endpoint, platform.S3PublicBaseURL = config.Enabled, config.Endpoint, config.PublicBaseURL
			platform.S3Region, platform.S3Bucket, platform.S3ForcePathStyle = config.Region, config.Bucket, config.ForcePathStyle
			platform.S3AccessKeyID, platform.S3SecretAccessKey = config.AccessKeyID, config.SecretAccessKey
			_, err := tx.NewUpdate().Model(platform).
				Column("s3_enabled", "s3_endpoint", "s3_public_base_url", "s3_region", "s3_bucket", "s3_access_key_id", "s3_secret_access_key", "s3_force_path_style").WherePK().Exec(ctx)
			return err
		})
	}
	if config.Enabled {
		return save(ctx)
	}
	// 关闭对象存储时在部署准入锁内确认只有本服务器运行，期间新的服务端进程不能加入部署。
	var settings DeploymentSettings
	err := serverinstanceaction.WithAdmissionLock(ctx, a.db, func(ctx context.Context, running []serverinstanceaction.ServerInstance) error {
		if len(running) > 1 {
			return ErrStorageRequiredByServers
		}
		var err error
		settings, err = save(ctx)
		return err
	})
	return settings, err
}

// UpdateEmail 校验后由仍有效的平台管理员保存邮件发送配置，主机为空时关闭邮件发送。
func (a *UpdateDeploymentAction) UpdateEmail(ctx context.Context, operator *servermodels.AccountIdentity, input mail.Config) (DeploymentSettings, error) {
	config := mail.Config{
		Host: strings.TrimSpace(input.Host), Port: input.Port, Username: strings.TrimSpace(input.Username), Password: input.Password,
		Security: strings.ToLower(strings.TrimSpace(input.Security)), FromAddress: strings.TrimSpace(input.FromAddress),
	}
	if config.Host != "" {
		fields := map[string]common.FieldCode{}
		if config.Port < 1 || config.Port > 65535 {
			fields["port"] = ValidationSMTPPortInvalid
		}
		if config.Security != "starttls" && config.Security != "tls" && config.Security != "none" {
			fields["security"] = ValidationSMTPSecurityInvalid
		}
		if (config.Username == "") != (config.Password == "") {
			fields["password"] = ValidationSMTPCredentialsIncomplete
		}
		if !str.IsEmail(config.FromAddress) {
			fields["fromAddress"] = ValidationSMTPFromAddressInvalid
		}
		if len(fields) > 0 {
			return DeploymentSettings{}, &common.FieldError{Fields: fields}
		}
	}
	return a.update(ctx, operator, "email", func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		platform.SMTPHost, platform.SMTPPort, platform.SMTPSecurity = config.Host, config.Port, config.Security
		platform.SMTPUsername, platform.SMTPPassword, platform.SMTPFromAddress = config.Username, config.Password, config.FromAddress
		_, err := tx.NewUpdate().Model(platform).
			Column("smtp_host", "smtp_port", "smtp_username", "smtp_password", "smtp_security", "smtp_from_address").WherePK().Exec(ctx)
		return err
	})
}

// UpdateBranding 校验后由仍有效的平台管理员保存部署品牌：产品名称只保留构建品牌已有语言的非空值，网站图标为不超过 512 KB 的 PNG 图片。
func (a *UpdateDeploymentAction) UpdateBranding(ctx context.Context, operator *servermodels.AccountIdentity, input DeploymentBranding) (DeploymentSettings, error) {
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
	if len(fields) > 0 {
		return DeploymentSettings{}, &common.FieldError{Fields: fields}
	}
	if len(branding.Icon) == 0 {
		branding.Icon = nil
	}
	return a.update(ctx, operator, "branding", func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		platform.BrandNames, platform.BrandSDKName = maps.Clone(branding.Names), branding.SDKName
		_, err := tx.NewUpdate().Model(platform).
			Column("brand_names", "brand_sdk_name").
			Set("brand_icon = ?", branding.Icon).WherePK().Exec(ctx)
		return err
	})
}

// UpdateHome 由仍有效的平台管理员保存产品首页是否展示自部署介绍。
func (a *UpdateDeploymentAction) UpdateHome(ctx context.Context, operator *servermodels.AccountIdentity, selfHost bool) (DeploymentSettings, error) {
	return a.update(ctx, operator, "home", func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		platform.HomeSelfHost = selfHost
		_, err := tx.NewUpdate().Model(platform).Column("home_self_host").WherePK().Exec(ctx)
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

// Capabilities 返回平台当前生效的能力，部署配置页据此说明品牌与产品首页配置是否生效。
func (q *DeploymentSettingsQuery) Capabilities(ctx context.Context) (domain.Capabilities, error) {
	return licenseaction.Capabilities(ctx, q.db)
}
