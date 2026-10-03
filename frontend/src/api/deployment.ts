/** 部署管理调用：部署概况、实例授权、部署设置、部署账号、部署工作区、业务使用和运行状态。 */
import {
  ActivateInstanceLicense,
  ActivateInstanceLicenseOnline,
  DeactivateDeploymentAccount,
  GetDeploymentRuntimeStatus,
  GetDeploymentUsage,
  GetDeploymentOverview,
  GetDeploymentSettings,
  GetInstanceLicense,
  GrantDeploymentAdmin,
  ListDeploymentAccounts,
  ListDeploymentFailedTasks,
  ListDeploymentWorkspaceUsage,
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
  DeploymentUsageSort,
  DeploymentWorkspaceSort,
  WorkspaceStatus,
  type DeploymentAccountListInput,
  type DeploymentFailedTaskListInput,
  type DeploymentWorkspaceUsageListInput,
  type DeploymentWorkspaceListInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"

const listDeploymentAccountsBound = bind(ListDeploymentAccounts)
const listDeploymentWorkspacesBound = bind(ListDeploymentWorkspaces)
const listDeploymentWorkspaceUsageBound = bind(ListDeploymentWorkspaceUsage)
const listDeploymentFailedTasksBound = bind(ListDeploymentFailedTasks)

/** 读取部署实例标识、规模、活跃趋势和实例能力。 */
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

/** 读取部署整体最近若干天的客服业务使用指标。 */
export const getDeploymentUsage = bind(GetDeploymentUsage)

/** 读取各工作区最近若干天的客服业务使用指标，天数缺省为 30，排序缺省按服务周期数。 */
export function listDeploymentWorkspaceUsage(query: Partial<DeploymentWorkspaceUsageListInput>, signal?: AbortSignal) {
  return listDeploymentWorkspaceUsageBound(
    {
      days: query.days ?? 30,
      sort: query.sort ?? DeploymentUsageSort.DeploymentUsageSortServiceSessions,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 读取服务端版本与后台任务各队列的运行概况。 */
export const getDeploymentRuntimeStatus = bind(GetDeploymentRuntimeStatus)

/** 读取近 7 天内失败与等待重试的后台任务。 */
export function listDeploymentFailedTasks(query: Partial<DeploymentFailedTaskListInput>, signal?: AbortSignal) {
  return listDeploymentFailedTasksBound({ page: query.page ?? 1, pageSize: query.pageSize ?? 50 }, signal)
}
