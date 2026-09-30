//go:build server

package role

import "errors"

// MaxRolesPerOrganization 表示单个企业允许创建的角色总数。
const MaxRolesPerOrganization = 20

var (
	// ErrNotFound 表示当前企业中不存在指定角色。
	ErrNotFound = errors.New("role not found")
	// ErrAdminImmutable 表示管理员角色不可修改。
	ErrAdminImmutable = errors.New("administrator role is immutable")
	// ErrBuiltInDeleteForbidden 表示内置角色不可删除。
	ErrBuiltInDeleteForbidden = errors.New("built-in role cannot be deleted")
	// ErrLimitReached 表示企业角色总数已达到上限。
	ErrLimitReached = errors.New("role limit reached")
	// ErrInUse 表示角色仍有关联成员。
	ErrInUse = errors.New("role is in use")
	// ErrAssignmentInvalid 表示角色归属调整参数无效。
	ErrAssignmentInvalid = errors.New("role assignment invalid")
	// ErrLastActiveAdministrator 表示企业至少需要保留一名账号正常的真人管理员。
	ErrLastActiveAdministrator = errors.New("organization requires an active administrator")
)
