package domain

// WebAppPath 是 Web 应用相对部署地址的访问路径，应用页面以哈希路由位于其后。
const WebAppPath = "/app/"

// RegistrationPolicy 定义部署的账号注册策略。
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

// WorkspaceCreationPolicy 定义部署内可以创建工作区的账号范围。
type WorkspaceCreationPolicy string

const (
	// WorkspaceCreationPolicyAnyAccount 允许所有有效账号创建工作区。
	WorkspaceCreationPolicyAnyAccount WorkspaceCreationPolicy = "any_account"
	// WorkspaceCreationPolicyDeploymentAdmin 只允许部署管理员创建工作区。
	WorkspaceCreationPolicyDeploymentAdmin WorkspaceCreationPolicy = "deployment_admin"
)

// Valid 判断工作区创建策略是否为已定义取值。
func (p WorkspaceCreationPolicy) Valid() bool {
	return p == WorkspaceCreationPolicyAnyAccount || p == WorkspaceCreationPolicyDeploymentAdmin
}

// Allows 判断该策略是否允许指定账号创建工作区。
func (p WorkspaceCreationPolicy) Allows(deploymentAdmin bool) bool {
	return p == WorkspaceCreationPolicyAnyAccount || deploymentAdmin
}

// InstanceCapabilities 定义部署实例当前生效的能力取值。
type InstanceCapabilities struct {
	// WorkspaceLimit 是部署内工作区数量上限，0 表示不限。
	WorkspaceLimit int
	// CustomBranding 表示是否允许应用部署品牌配置。
	CustomBranding bool
}

// FreeInstanceCapabilities 返回未激活授权的实例使用的能力取值。
func FreeInstanceCapabilities() InstanceCapabilities {
	return InstanceCapabilities{WorkspaceLimit: 1}
}

// AllowsAnotherWorkspace 判断部署已有 count 个工作区时是否还能再创建一个。
func (c InstanceCapabilities) AllowsAnotherWorkspace(count int) bool {
	return c.WorkspaceLimit == 0 || count < c.WorkspaceLimit
}

// LicenseStatus 定义实例授权状态。
type LicenseStatus string

const (
	// LicenseStatusNone 表示实例未激活授权。
	LicenseStatusNone LicenseStatus = "none"
	// LicenseStatusActive 表示授权在有效期内。
	LicenseStatusActive LicenseStatus = "active"
	// LicenseStatusExpired 表示授权已到期，实例按免费取值运行。
	LicenseStatusExpired LicenseStatus = "expired"
)
