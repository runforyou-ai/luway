/** 平台管理调用：平台概览、授权、平台设置、平台账号、平台工作区、业务使用和运行状态。 */
import {
  ActivateLicense,
  ActivateLicenseOnline,
  DeactivatePlatformAccount,
  GetPlatformDiagnostics,
  GetPlatformRuntimeStatus,
  GetPlatformUsage,
  GetPlatformOverview,
  GetPlatformSettings,
  GetLicense,
  GrantPlatformAdmin,
  ListPlatformAccounts,
  ListPlatformFailedTasks,
  ListPlatformServerErrors,
  ListPlatformWorkspaceUsage,
  ListPlatformWorkspaces,
  ReactivatePlatformAccount,
  ResumePlatformWorkspace,
  RevokePlatformAdmin,
  SuspendPlatformWorkspace,
  SyncLicense,
  UpdatePlatformSettings,
  UpdatePlatformTimeZone,
  UpdatePlatformTelemetry,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  AccountStatus,
  PlatformUsageSort,
  PlatformWorkspaceSort,
  WorkspaceStatus,
  type PlatformAccountListInput,
  type PlatformFailedTaskListInput,
  type PlatformServerErrorListInput,
  type PlatformWorkspaceUsageListInput,
  type PlatformWorkspaceListInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"

const listPlatformAccountsBound = bind(ListPlatformAccounts)
const listPlatformWorkspacesBound = bind(ListPlatformWorkspaces)
const listPlatformWorkspaceUsageBound = bind(ListPlatformWorkspaceUsage)
const listPlatformFailedTasksBound = bind(ListPlatformFailedTasks)
const listPlatformServerErrorsBound = bind(ListPlatformServerErrors)

/** 读取服务器标识、规模、活跃趋势、授权状态和平台能力。 */
export const getPlatformOverview = bind(GetPlatformOverview)

/** 读取服务器标识与授权状态。 */
export const getLicense = bind(GetLicense)

/** 用授权码离线激活或替换授权。 */
export const activateLicense = bind(ActivateLicense)

/** 用激活码在线激活授权。 */
export const activateLicenseOnline = bind(ActivateLicenseOnline)

/** 立即向授权服务登记服务器并拉取最新授权。 */
export const syncLicense = bind(SyncLicense)

/** 读取平台注册策略、工作区创建策略、平台时区、运行指标上报开关和每日赠送积分。 */
export const getPlatformSettings = bind(GetPlatformSettings)

/** 修改平台注册策略和工作区创建策略。 */
export const updatePlatformSettings = bind(UpdatePlatformSettings)

/** 修改平台时区，服务端按新时区在后台重建运营数据。 */
export const updatePlatformTimeZone = bind(UpdatePlatformTimeZone)

/** 开启或关闭运行指标上报。 */
export const updatePlatformTelemetry = bind(UpdatePlatformTelemetry)

/** 停用其他账号并使其登录会话失效。 */
export const deactivatePlatformAccount = bind(DeactivatePlatformAccount)

/** 恢复已停用的其他账号。 */
export const reactivatePlatformAccount = bind(ReactivatePlatformAccount)

/** 把其他账号设为平台管理员。 */
export const grantPlatformAdmin = bind(GrantPlatformAdmin)

/** 撤销其他账号的平台管理员身份。 */
export const revokePlatformAdmin = bind(RevokePlatformAdmin)

/** 读取平台账号列表，状态缺省为有效账号。 */
export function listPlatformAccounts(query: Partial<PlatformAccountListInput>, signal?: AbortSignal) {
  return listPlatformAccountsBound(
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
export const suspendPlatformWorkspace = bind(SuspendPlatformWorkspace)

/** 恢复已暂停的工作区。 */
export const resumePlatformWorkspace = bind(ResumePlatformWorkspace)

/** 读取平台内的全部工作区及其状态和当前规模，状态缺省为全部，排序缺省按创建时间。 */
export function listPlatformWorkspaces(query: Partial<PlatformWorkspaceListInput>, signal?: AbortSignal) {
  return listPlatformWorkspacesBound(
    {
      query: query.query ?? "",
      status: query.status ?? WorkspaceStatus.$zero,
      sort: query.sort ?? PlatformWorkspaceSort.PlatformWorkspaceSortCreatedAt,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 读取平台整体最近若干天的客服业务使用指标与平台模型用量。 */
export const getPlatformUsage = bind(GetPlatformUsage)

/** 读取各工作区最近若干天的客服业务使用指标与平台模型用量，天数缺省为 30，排序缺省按服务周期数。 */
export function listPlatformWorkspaceUsage(query: Partial<PlatformWorkspaceUsageListInput>, signal?: AbortSignal) {
  return listPlatformWorkspaceUsageBound(
    {
      days: query.days ?? 30,
      sort: query.sort ?? PlatformUsageSort.PlatformUsageSortServiceSessions,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 读取服务端进程、外部依赖与后台任务各队列的运行状态。 */
export const getPlatformRuntimeStatus = bind(GetPlatformRuntimeStatus)

/** 生成平台诊断信息，不含密码、密钥与业务内容。 */
export const getPlatformDiagnostics = bind(GetPlatformDiagnostics)

/** 读取近 7 天内失败与等待重试的后台任务。 */
export function listPlatformFailedTasks(query: Partial<PlatformFailedTaskListInput>, signal?: AbortSignal) {
  return listPlatformFailedTasksBound({ page: query.page ?? 1, pageSize: query.pageSize ?? 50 }, signal)
}

/** 读取近 7 天的服务端错误记录。 */
export function listPlatformServerErrors(query: Partial<PlatformServerErrorListInput>, signal?: AbortSignal) {
  return listPlatformServerErrorsBound({ page: query.page ?? 1, pageSize: query.pageSize ?? 50 }, signal)
}
