package domain

// PermissionCode 定义可分配给角色的权限。
type PermissionCode string

const (
	// PermissionWorkspaceManage 管理工作区：通用设置、成员与团队、邀请、角色、模型与集成、工作区电脑。
	PermissionWorkspaceManage PermissionCode = "workspace.manage"
	// PermissionAIEmployeesManage 管理 AI 员工、评测、知识库与全部待补知识。
	PermissionAIEmployeesManage PermissionCode = "ai_employees.manage"
	// PermissionCustomerServiceManage 管理渠道与客服设置。
	PermissionCustomerServiceManage PermissionCode = "customer_service.manage"
	// PermissionExternalContactsManage 新建、编辑与删除外部联系人。
	PermissionExternalContactsManage PermissionCode = "external_contacts.manage"
	// PermissionReportsView 查看 AI 表现与团队表现报表。
	PermissionReportsView PermissionCode = "reports.view"
)

// PermissionDefinition 描述一项预定义权限。
type PermissionDefinition struct {
	Code PermissionCode
}

// permissionDefinitions 是按界面顺序排列的预定义权限。
var permissionDefinitions = []PermissionDefinition{
	{Code: PermissionWorkspaceManage},
	{Code: PermissionAIEmployeesManage},
	{Code: PermissionCustomerServiceManage},
	{Code: PermissionExternalContactsManage},
	{Code: PermissionReportsView},
}

// PermissionDefinitions 返回按界面顺序排列的权限目录。
func PermissionDefinitions() []PermissionDefinition {
	return append([]PermissionDefinition(nil), permissionDefinitions...)
}

// IsPermissionCode 判断权限代码是否受支持。
func IsPermissionCode(code PermissionCode) bool {
	for _, definition := range permissionDefinitions {
		if definition.Code == code {
			return true
		}
	}
	return false
}
