/** 平台管理调用：平台概览、授权、平台设置、部署配置、平台账号、平台工作区、业务使用和运行状态。 */
import {
  AccountStatus,
  PlatformUsageSort,
  PlatformWorkspaceSort,
  type PlatformAccountListInput,
  type PlatformFailedTaskListInput,
  type PlatformServerLogListInput,
  type PlatformWorkspaceUsageListInput,
  type PlatformWorkspaceListInput,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

/** 读取平台规模与活跃趋势。 */
export const getPlatformOverview = ops.getPlatformOverview

/** 读取服务器标识、授权状态与当前生效的能力。 */
export const getLicense = ops.getLicense

/** 用授权码离线激活或替换授权。 */
export const activateLicense = ops.activateLicense

/** 用激活码在线激活授权。 */
export const activateLicenseOnline = ops.activateLicenseOnline

/** 立即向授权服务登记服务器并拉取最新授权。 */
export const syncLicense = ops.syncLicense

/** 读取平台注册策略与工作区创建策略。 */
export const getPlatformSettings = ops.getPlatformSettings

/** 修改平台注册策略和工作区创建策略。 */
export const updatePlatformSettings = ops.updatePlatformSettings

/** 读取部署名称、部署地址与证书、平台时区、上报开关、对象存储、邮件发送、部署品牌与产品首页配置。 */
export const getPlatformDeployment = ops.getPlatformDeployment

/** 修改部署名称、平台时区与上报开关。 */
export const updatePlatformDeploymentBasics = ops.updatePlatformDeploymentBasics

/** 修改部署地址与证书，HTTPS 部署地址需要自动签发证书时服务端先签发再保存。 */
export const updatePlatformAddress = ops.updatePlatformAddress

/** 修改对象存储配置，开启时服务端先确认能访问存储桶。 */
export const updatePlatformStorage = ops.updatePlatformStorage

/** 修改 SMTP 邮件发送配置，主机为空时关闭邮件发送。 */
export const updatePlatformEmail = ops.updatePlatformEmail

/** 修改部署品牌，图标为 PNG 的 Base64 内容，空字符串表示沿用构建图标。 */
export const updatePlatformBranding = ops.updatePlatformBranding

/** 修改产品首页配置，首页提供价格区块时生效。 */
export const updatePlatformHome = ops.updatePlatformHome

/** 停用其他账号并使其登录会话失效。 */
export const deactivatePlatformAccount = ops.deactivatePlatformAccount

/** 恢复已停用的其他账号。 */
export const reactivatePlatformAccount = ops.reactivatePlatformAccount

/** 把其他账号设为平台管理员。 */
export const grantPlatformAdmin = ops.grantPlatformAdmin

/** 撤销其他账号的平台管理员身份。 */
export const revokePlatformAdmin = ops.revokePlatformAdmin

/** 读取平台账号列表，状态缺省为有效账号。 */
export function listPlatformAccounts(query: Partial<PlatformAccountListInput>, signal?: AbortSignal) {
  return ops.listPlatformAccounts(
    {
      query: query.query ?? "",
      status: query.status ?? AccountStatus.Active,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 暂停工作区：成员无法进入，渠道停止接待客户，后台任务挂起。 */
export const suspendPlatformWorkspace = ops.suspendPlatformWorkspace

/** 恢复已暂停的工作区。 */
export const resumePlatformWorkspace = ops.resumePlatformWorkspace

/** 读取平台内的全部工作区及其状态和当前规模，状态缺省为全部，排序缺省按创建时间。 */
export function listPlatformWorkspaces(query: Partial<PlatformWorkspaceListInput>, signal?: AbortSignal) {
  return ops.listPlatformWorkspaces(
    {
      query: query.query ?? "",
      status: query.status,
      sort: query.sort ?? PlatformWorkspaceSort.CreatedAt,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 读取平台整体最近若干天的客服业务使用指标与平台模型用量。 */
export const getPlatformUsage = ops.getPlatformUsage

/** 读取各工作区最近若干天的客服业务使用指标与平台模型用量，天数缺省为 30，排序缺省按服务周期数。 */
export function listPlatformWorkspaceUsage(query: Partial<PlatformWorkspaceUsageListInput>, signal?: AbortSignal) {
  return ops.listPlatformWorkspaceUsage(
    {
      days: query.days ?? 30,
      sort: query.sort ?? PlatformUsageSort.ServiceSessions,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 读取服务端进程、外部依赖与后台任务各队列的运行状态。 */
export const getPlatformRuntimeStatus = ops.getPlatformRuntimeStatus

/** 生成平台诊断信息，不含密码、密钥与业务内容。 */
export const getPlatformDiagnostics = ops.getPlatformDiagnostics

/** 读取近 7 天内失败与等待重试的后台任务。 */
export function listPlatformFailedTasks(query: Partial<PlatformFailedTaskListInput>, signal?: AbortSignal) {
  return ops.listPlatformFailedTasks({ page: query.page ?? 1, pageSize: query.pageSize ?? 50 }, signal)
}

/** 按筛选条件以时间倒序读取近 30 天的服务端日志，cursor 为空时从最新的日志读起。 */
export function listPlatformServerLogs(query: Partial<PlatformServerLogListInput>, signal?: AbortSignal) {
  return ops.listPlatformServerLogs(
    {
      cursor: query.cursor ?? "",
      pageSize: query.pageSize ?? 50,
      minLevel: query.minLevel,
      instanceId: query.instanceId ?? "",
      workspaceId: query.workspaceId ?? "",
      entry: query.entry ?? "",
      traceId: query.traceId ?? "",
    },
    signal,
  )
}
