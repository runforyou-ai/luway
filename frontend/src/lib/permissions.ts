/** 按当前成员所属角色的权限判断功能是否可用。 */
import type { CurrentUser, PermissionCode } from "@/api"

/** 判断当前成员所属角色是否拥有指定权限；未指定权限时视为可用。 */
export function hasPermission(user: Pick<CurrentUser, "permissions">, code?: PermissionCode) {
  return !code || user.permissions.includes(code)
}
