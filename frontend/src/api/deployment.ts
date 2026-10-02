/** 部署管理调用：部署概况、注册与创建策略、部署账号和部署工作区。 */
import {
  DeactivateDeploymentAccount,
  GetDeploymentOverview,
  GetDeploymentSettings,
  GrantDeploymentAdmin,
  ListDeploymentAccounts,
  ListDeploymentWorkspaces,
  ReactivateDeploymentAccount,
  RevokeDeploymentAdmin,
  UpdateDeploymentSettings,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  AccountStatus,
  type DeploymentAccountListInput,
  type DeploymentWorkspaceListInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"

const listDeploymentAccountsBound = bind(ListDeploymentAccounts)
const listDeploymentWorkspacesBound = bind(ListDeploymentWorkspaces)

/** 读取部署实例标识、服务端版本、规模和实例能力。 */
export const getDeploymentOverview = bind(GetDeploymentOverview)

/** 读取部署注册策略和工作区创建策略。 */
export const getDeploymentSettings = bind(GetDeploymentSettings)

/** 修改部署注册策略和工作区创建策略。 */
export const updateDeploymentSettings = bind(UpdateDeploymentSettings)

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

/** 读取部署内的全部工作区。 */
export function listDeploymentWorkspaces(query: Partial<DeploymentWorkspaceListInput>, signal?: AbortSignal) {
  return listDeploymentWorkspacesBound(
    {
      query: query.query ?? "",
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}
