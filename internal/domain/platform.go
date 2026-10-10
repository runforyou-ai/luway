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

// WorkspaceCreationPolicy 定义平台内可以创建工作区的账号范围。
type WorkspaceCreationPolicy string

const (
	// WorkspaceCreationPolicyAnyAccount 允许所有有效账号创建工作区。
	WorkspaceCreationPolicyAnyAccount WorkspaceCreationPolicy = "any_account"
	// WorkspaceCreationPolicyPlatformAdmin 只允许平台管理员创建工作区。
	WorkspaceCreationPolicyPlatformAdmin WorkspaceCreationPolicy = "platform_admin"
)

// Valid 判断工作区创建策略是否为已定义取值。
func (p WorkspaceCreationPolicy) Valid() bool {
	return p == WorkspaceCreationPolicyAnyAccount || p == WorkspaceCreationPolicyPlatformAdmin
}

// Allows 判断该策略是否允许指定账号创建工作区。
func (p WorkspaceCreationPolicy) Allows(platformAdmin bool) bool {
	return p == WorkspaceCreationPolicyAnyAccount || platformAdmin
}

// Capabilities 定义平台当前生效的能力取值。
type Capabilities struct {
	// WorkspaceLimit 是平台内工作区数量上限，0 表示不限。
	WorkspaceLimit int
	// CustomBranding 表示是否允许应用部署品牌配置。
	CustomBranding bool
	// PushRelay 表示是否经 control 向移动设备发送离线推送。
	PushRelay bool
}

// FreeCapabilities 返回未激活授权的平台使用的能力取值。
func FreeCapabilities() Capabilities {
	return Capabilities{WorkspaceLimit: 1}
}

// AllowsAnotherWorkspace 判断平台已有 count 个工作区时是否还能再创建一个。
func (c Capabilities) AllowsAnotherWorkspace(count int) bool {
	return c.WorkspaceLimit == 0 || count < c.WorkspaceLimit
}

// LicenseStatus 定义授权状态。
type LicenseStatus string

const (
	// LicenseStatusNone 表示平台未激活授权。
	LicenseStatusNone LicenseStatus = "none"
	// LicenseStatusActive 表示授权在有效期内。
	LicenseStatusActive LicenseStatus = "active"
	// LicenseStatusExpired 表示授权已到期，平台按免费取值运行。
	LicenseStatusExpired LicenseStatus = "expired"
)

// CertificateSource 定义部署地址的 HTTPS 证书来源。
type CertificateSource string

const (
	// CertificateSourceACME 表示证书经 ACME 服务自动签发并续期。
	CertificateSourceACME CertificateSource = "acme"
	// CertificateSourceUpload 表示证书由平台管理员上传。
	CertificateSourceUpload CertificateSource = "upload"
)
