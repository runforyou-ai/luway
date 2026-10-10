/** 角色与权限的共享界面文案映射。 */
import type { TFunction } from "i18next"

import { PermissionCode, RoleKind, type Role } from "@/api"

/** 返回角色显示名称。 */
export function roleDisplayName(
  role: Pick<Role, "kind" | "name">,
  t: TFunction<"common">,
) {
  switch (role.kind) {
    case RoleKind.Admin:
      return t("roles.admin")
    case RoleKind.CustomerService:
      return t("roles.customerService")
    case RoleKind.Member:
      return t("roles.member")
    case RoleKind.Custom:
      return role.name
    default:
      console.warn("未知的角色类型", role.kind)
      return role.name
  }
}

/** 返回角色面向使用者的说明。 */
export function roleDescription(
  role: Role,
  t: TFunction<"settings">,
) {
  switch (role.kind) {
    case RoleKind.Custom:
      return role.description
    case RoleKind.Admin:
      return t("roles.kindsDescriptions.admin")
    case RoleKind.CustomerService:
      return t("roles.kindsDescriptions.customerService")
    case RoleKind.Member:
      return t("roles.kindsDescriptions.member")
    default:
      console.warn("未知的角色类型", role.kind)
      return role.description
  }
}

/** 返回权限名称。 */
export function permissionLabel(code: PermissionCode, t: TFunction<"settings">) {
  switch (code) {
    case PermissionCode.PermissionWorkspaceManage:
      return t("roles.permissions.items.workspace.label")
    case PermissionCode.PermissionAIEmployeesManage:
      return t("roles.permissions.items.aiEmployees.label")
    case PermissionCode.PermissionCustomerServiceManage:
      return t("roles.permissions.items.customerService.label")
    case PermissionCode.PermissionExternalContactsManage:
      return t("roles.permissions.items.externalContacts.label")
    case PermissionCode.PermissionReportsView:
      return t("roles.permissions.items.reports.label")
    default:
      console.warn("未知的权限", code)
      return String(code)
  }
}

/** 返回权限包含的功能说明。 */
export function permissionDescription(code: PermissionCode, t: TFunction<"settings">) {
  switch (code) {
    case PermissionCode.PermissionWorkspaceManage:
      return t("roles.permissions.items.workspace.description")
    case PermissionCode.PermissionAIEmployeesManage:
      return t("roles.permissions.items.aiEmployees.description")
    case PermissionCode.PermissionCustomerServiceManage:
      return t("roles.permissions.items.customerService.description")
    case PermissionCode.PermissionExternalContactsManage:
      return t("roles.permissions.items.externalContacts.description")
    case PermissionCode.PermissionReportsView:
      return t("roles.permissions.items.reports.description")
    default:
      console.warn("未知的权限", code)
      return ""
  }
}
