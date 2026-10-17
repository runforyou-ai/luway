/** 平台后台路由前缀与核心页面地址。 */
export const adminRootPath = "/admin"
export const adminLoginPath = "/admin/login"
export const adminDashboardPath = "/admin/dashboard"
export const adminWorkspacesPath = "/admin/workspaces"
export const adminAccountsPath = "/admin/accounts"
export const adminLicensePath = "/admin/license"

/** 概览入口地址，进入后重定向到业务使用。 */
export const adminOverviewPath = "/admin/overview"

/** 旧的平台规模地址，规模内容已汇入仪表板，仅用于重定向。 */
export const adminOverviewScalePath = `${adminOverviewPath}/scale`

/** 监控分组下的页面，按导航顺序排列；平台规模已汇入仪表板。 */
export const adminOverviewSections = ["usage", "runtime", "errors"] as const
export type AdminOverviewSection = (typeof adminOverviewSections)[number]

/** 返回监控下指定页面的地址。 */
export function adminOverviewSectionPath(section: AdminOverviewSection) {
  return `${adminOverviewPath}/${section}`
}

/** 站点设置（系统组主入口）地址。 */
export const adminSystemPath = "/admin/system"

/** 系统组中与站点设置并列的集成页，按导航顺序排列。 */
export const adminSystemSections = ["storage", "email", "wechat"] as const
export type AdminSystemSection = (typeof adminSystemSections)[number]

/** 返回系统组集成页地址。 */
export function adminSystemSectionPath(section: AdminSystemSection) {
  return `${adminSystemPath}/${section}`
}

/** 旧的系统设置地址，仅用于重定向。 */
export const adminDeploymentPath = "/admin/deployment"
