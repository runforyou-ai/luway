/** 部署管理调用：部署概况、实例授权、部署设置、部署账号和部署工作区。 */
import {
  ActivateInstanceLicense,
  ActivateInstanceLicenseOnline,
  DeactivateDeploymentAccount,
  GetDeploymentOverview,
  GetDeploymentSettings,
  GetInstanceLicense,
  GrantDeploymentAdmin,
  ListDeploymentAccounts,
  ListDeploymentWorkspaces,
  ReactivateDeploymentAccount,
  ResumeDeploymentWorkspace,
  RevokeDeploymentAdmin,
  SuspendDeploymentWorkspace,
  SyncInstanceLicense,
  UpdateDeploymentSettings,
  UpdateDeploymentStatisticsTimeZone,
  UpdateDeploymentTelemetry,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  AccountStatus,
  DeploymentWorkspaceSort,
  WorkspaceStatus,
  type DeploymentAccountListInput,
  type DeploymentWorkspaceListInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"

const listDeploymentAccountsBound = bind(ListDeploymentAccounts)
const listDeploymentWorkspacesBound = bind(ListDeploymentWorkspaces)

/** 读取部署实例标识、服务端版本、规模、活跃趋势和实例能力。 */
export const getDeploymentOverview = bind(GetDeploymentOverview)

/** 读取实例标识与实例授权状态。 */
export const getInstanceLicense = bind(GetInstanceLicense)

/** 用授权码离线激活或替换实例授权。 */
export const activateInstanceLicense = bind(ActivateInstanceLicense)

/** 用激活码在线激活实例授权。 */
export const activateInstanceLicenseOnline = bind(ActivateInstanceLicenseOnline)

/** 立即向授权服务登记实例并拉取最新授权。 */
export const syncInstanceLicense = bind(SyncInstanceLicense)

/** 读取部署注册策略、工作区创建策略、统计时区和运行指标上报开关。 */
export const getDeploymentSettings = bind(GetDeploymentSettings)

/** 修改部署注册策略和工作区创建策略。 */
export const updateDeploymentSettings = bind(UpdateDeploymentSettings)

/** 修改运营数据统计时区，服务端按新时区在后台重建运营数据。 */
export const updateDeploymentStatisticsTimeZone = bind(UpdateDeploymentStatisticsTimeZone)

/** 开启或关闭运行指标上报。 */
export const updateDeploymentTelemetry = bind(UpdateDeploymentTelemetry)

/** 停用其他账号并使其登录会话失效。 */
export const deactivateDeploymentAccount = bind(DeactivateDeploymentAccount)

/** 恢复已停用的其他账号。 */
export const reactivateDeploymentAccount = bind(ReactivateDeploymentAccount)

/** 把其他账号设为部署管理员。 */
export const grantDeploymentAdmin = bind(GrantDeploymentAdmin)

/** 撤销其他账号的部署管理员身份。 */
export const revokeDeploymentAdmin = bind(RevokeDeploymentAdmin)

/** 读取部署账号列表，状态缺省为有效账号。 */
export function listDeploymentAccounts(query: Partial<DeploymentAccountListInput>, signal?: AbortSignal) {
  return listDeploymentAccountsBound(
    {
      query: query.query ?? "",
      status: query.status ?? AccountStatus.AccountStatusActive,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 暂停工作区：成员无法进入，渠道停止接待客户，后台任务挂起。 */
export const suspendDeploymentWorkspace = bind(SuspendDeploymentWorkspace)

/** 恢复已暂停的工作区。 */
export const resumeDeploymentWorkspace = bind(ResumeDeploymentWorkspace)

/** 读取部署内的全部工作区及其状态和当前规模，状态缺省为全部，排序缺省按创建时间。 */
export function listDeploymentWorkspaces(query: Partial<DeploymentWorkspaceListInput>, signal?: AbortSignal) {
  return listDeploymentWorkspacesBound(
    {
      query: query.query ?? "",
      status: query.status ?? WorkspaceStatus.$zero,
      sort: query.sort ?? DeploymentWorkspaceSort.DeploymentWorkspaceSortCreatedAt,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}
