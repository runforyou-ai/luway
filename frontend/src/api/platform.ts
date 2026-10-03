/** 平台管理调用：平台概览、授权、平台设置、平台账号和平台工作区。 */
import {
  ActivateLicense,
  ActivateLicenseOnline,
  DeactivatePlatformAccount,
  GetPlatformOverview,
  GetPlatformSettings,
  GetLicense,
  GrantPlatformAdmin,
  ListPlatformAccounts,
  ListPlatformWorkspaces,
  ReactivatePlatformAccount,
  ResumePlatformWorkspace,
  RevokePlatformAdmin,
  SuspendPlatformWorkspace,
  SyncLicense,
  UpdatePlatformSettings,
  UpdatePlatformStatisticsTimeZone,
  UpdatePlatformTelemetry,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  AccountStatus,
  PlatformWorkspaceSort,
  WorkspaceStatus,
  type PlatformAccountListInput,
  type PlatformWorkspaceListInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"

const listPlatformAccountsBound = bind(ListPlatformAccounts)
const listPlatformWorkspacesBound = bind(ListPlatformWorkspaces)

/** 读取服务器标识、服务端版本、规模、活跃趋势和平台能力。 */
export const getPlatformOverview = bind(GetPlatformOverview)

/** 读取服务器标识与授权状态。 */
export const getLicense = bind(GetLicense)

/** 用授权码离线激活或替换授权。 */
export const activateLicense = bind(ActivateLicense)

/** 用激活码在线激活授权。 */
export const activateLicenseOnline = bind(ActivateLicenseOnline)

/** 立即向授权服务登记服务器并拉取最新授权。 */
export const syncLicense = bind(SyncLicense)

/** 读取平台注册策略、工作区创建策略、统计时区和运行指标上报开关。 */
export const getPlatformSettings = bind(GetPlatformSettings)

/** 修改平台注册策略和工作区创建策略。 */
export const updatePlatformSettings = bind(UpdatePlatformSettings)

/** 修改运营数据统计时区，服务端按新时区在后台重建运营数据。 */
export const updatePlatformStatisticsTimeZone = bind(UpdatePlatformStatisticsTimeZone)

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
