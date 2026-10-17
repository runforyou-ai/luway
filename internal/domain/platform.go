package domain

// WebAppPath 是 Web 应用相对部署地址的访问路径，应用页面以哈希路由位于其后。
const WebAppPath = "/app/"

// RegistrationPolicy 定义平台的账号注册策略。
type RegistrationPolicy string

const (
	// RegistrationPolicyOpen 允许任何人在登录页注册账号。
	RegistrationPolicyOpen RegistrationPolicy = "open"
	// RegistrationPolicyInvitationOnly 只允许持有效邀请的受邀邮箱注册。
	RegistrationPolicyInvitationOnly RegistrationPolicy = "invitation_only"
)

// Valid 判断注册策略是否为已定义取值。
func (p RegistrationPolicy) Valid() bool {
	return p == RegistrationPolicyOpen || p == RegistrationPolicyInvitationOnly
}

// LicenseStatus 定义授权状态。
type LicenseStatus string

const (
	// LicenseStatusNone 表示平台未激活授权。
	LicenseStatusNone LicenseStatus = "none"
	// LicenseStatusActive 表示授权在有效期内，产品功能全开。
	LicenseStatusActive LicenseStatus = "active"
	// LicenseStatusExpired 表示授权已到期，按未授权运行。
	LicenseStatusExpired LicenseStatus = "expired"
)

// Licensed 判断授权是否处于有效期内。
func (s LicenseStatus) Licensed() bool {
	return s == LicenseStatusActive
}

// CertificateSource 定义部署地址的 HTTPS 证书来源。
type CertificateSource string

const (
	// CertificateSourceACME 表示证书经 ACME 服务自动签发并续期。
	CertificateSourceACME CertificateSource = "acme"
	// CertificateSourceUpload 表示证书由平台管理员上传。
	CertificateSourceUpload CertificateSource = "upload"
)
