//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Platform 表示 PostgreSQL 中唯一一行的平台记录、平台级策略与部署配置；网站图标 brand_icon 只由部署状态单独读取。
type Platform struct {
	bun.BaseModel `bun:"table:platforms,alias:pf"`

	ServerID                   string            `bun:"server_id,pk"`
	CreatedAt                  time.Time         `bun:"created_at,nullzero,default:now()"`
	UpdatedAt                  time.Time         `bun:"updated_at,nullzero,default:now()"`
	RegistrationPolicy         string            `bun:"registration_policy"`
	WorkspaceCreationPolicy    string            `bun:"workspace_creation_policy"`
	TimeZone                   string            `bun:"time_zone"`
	StatisticsRebuildPending   bool              `bun:"statistics_rebuild_pending"`
	ServerPrivateKey           []byte            `bun:"server_private_key"`
	TelemetryEnabled           bool              `bun:"telemetry_enabled"`
	ControlSyncedAt            *time.Time        `bun:"control_synced_at"`
	ControlFailedAt            *time.Time        `bun:"control_failed_at"`
	ControlError               string            `bun:"control_error"`
	DeploymentName             string            `bun:"deployment_name"`
	PublicURL                  string            `bun:"public_url"`
	S3Enabled                  bool              `bun:"s3_enabled"`
	S3Endpoint                 string            `bun:"s3_endpoint"`
	S3PublicBaseURL            string            `bun:"s3_public_base_url"`
	S3Region                   string            `bun:"s3_region"`
	S3Bucket                   string            `bun:"s3_bucket"`
	S3AccessKeyID              string            `bun:"s3_access_key_id"`
	S3SecretAccessKey          string            `bun:"s3_secret_access_key"`
	S3ForcePathStyle           bool              `bun:"s3_force_path_style"`
	SMTPHost                   string            `bun:"smtp_host"`
	SMTPPort                   int               `bun:"smtp_port"`
	SMTPUsername               string            `bun:"smtp_username"`
	SMTPPassword               string            `bun:"smtp_password"`
	SMTPSecurity               string            `bun:"smtp_security"`
	SMTPFromAddress            string            `bun:"smtp_from_address"`
	BrandNames                 map[string]string `bun:"brand_names,type:jsonb"`
	BrandSDKName               string            `bun:"brand_sdk_name"`
	HomeSelfHost               bool              `bun:"home_self_host"`
	CertificateSource          string            `bun:"certificate_source"`
	Certificate                string            `bun:"certificate"`
	CertificatePrivateKey      string            `bun:"certificate_private_key"`
	CertificateExpiresAt       *time.Time        `bun:"certificate_expires_at"`
	CertificateRenewalError    string            `bun:"certificate_renewal_error"`
	CertificateRenewalFailedAt *time.Time        `bun:"certificate_renewal_failed_at"`
	ACMEAccountKey             string            `bun:"acme_account_key"`
}
