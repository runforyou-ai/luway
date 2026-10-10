/** 角色与权限调用。 */
import * as ops from "@/api/generated/operations"

/** 读取成员表单与筛选使用的角色选项，并标出当前成员可以分配的角色。 */
export const listRoleOptions = ops.listRoleOptions

/** 读取角色、数量上限和权限目录。 */
export const listRoles = ops.listRoles

/** 读取角色详情。 */
export const getRole = ops.getRole

/** 创建自定义角色。 */
export const createRole = ops.createRole

/** 修改角色信息和权限。 */
export const updateRole = ops.updateRole

/** 在一个事务中批量调整真人和 AI 员工角色。 */
export const updateRoleAssignments = ops.updateRoleAssignments

/** 删除自定义角色。 */
export const deleteRole = ops.deleteRole
