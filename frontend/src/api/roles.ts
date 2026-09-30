/** 角色与权限调用。 */
import {
  CreateRole,
  DeleteRole,
  GetRole,
  ListRoles,
  UpdateRoleAssignments,
  UpdateRole,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import type {
  Role,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type RoleData = NonNullArrays<Role>

/** 读取角色、数量上限和权限目录。 */
export const listRoles = bind(ListRoles)

/** 读取角色详情。 */
export const getRole = bind(GetRole)

/** 创建自定义角色。 */
export const createRole = bind(CreateRole)

/** 修改角色信息和权限。 */
export const updateRole = bind(UpdateRole)

/** 在一个事务中批量调整真人和 AI 员工角色。 */
export const updateRoleAssignments = bind(UpdateRoleAssignments)

/** 删除自定义角色。 */
export const deleteRole = bind(DeleteRole)
