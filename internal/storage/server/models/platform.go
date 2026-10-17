//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/uptrace/bun"
)

// Platform 表示 PostgreSQL 中唯一一行的平台记录、平台级策略与部署配置；网站图标 brand_icon 只由部署状态单独读取。
type Platform struct {
	bun.BaseModel `bun:"table:platforms,alias:pf"`

	DeploymentID               string            `bun:"deployment_id,pk"`
	CreatedAt                  time.Time         `bun:"created_at,nullzero,default:now()"`
	UpdatedAt                  time.Time         `bun:"updated_at,nullzero,default:now()"`
	RegistrationPolicy         string            `bun:"registration_policy"`
	WorkspaceCreationQuota     int               `bun:"workspace_creation_quota"`
	DefaultWorkspaceID         string            `bun:"default_workspace_id,nullzero"`
	TimeZone                   string            `bun:"time_zone"`
	StatisticsRebuildPending   bool              `bun:"statistics_rebuild_pending"`
	DeploymentPrivateKey       []byte            `bun:"deployment_private_key"`
	ControlRegisteredAt        *time.Time        `bun:"control_registered_at"`
	DeploymentName             string            `bun:"deployment_name"`
	PublicURL                  string            `bun:"public_url"`
	S3Endpoint                 string            `bun:"s3_endpoint"`
	S3PublicBaseURL            string            `bun:"s3_public_base_url"`
	S3Region                   string            `bun:"s3_region"`
	S3Bucket                   string            `bun:"s3_bucket"`
	S3AccessKeyID              string            `bun:"s3_access_key_id"`
	S3SecretAccessKey          string            `bun:"s3_secret_access_key"`
	S3ForcePathStyle           bool              `bun:"s3_force_path_style"`
	EmailConfig                mail.Config       `bun:"email_config,type:jsonb"`
	BrandNames                 map[string]string `bun:"brand_names,type:jsonb"`
	BrandSDKName               string            `bun:"brand_sdk_name"`
	CertificateSource          string            `bun:"certificate_source"`
	Certificate                string            `bun:"certificate"`
	CertificatePrivateKey      string            `bun:"certificate_private_key"`
	CertificateExpiresAt       *time.Time        `bun:"certificate_expires_at"`
	CertificateRenewalError    string            `bun:"certificate_renewal_error"`
	CertificateRenewalFailedAt *time.Time        `bun:"certificate_renewal_failed_at"`
	ACMEAccountKey             string            `bun:"acme_account_key"`
}
