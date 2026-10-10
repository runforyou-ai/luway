/** 企业成员账号与通讯录同事目录调用。 */
import { UserStatus, type ColleagueListInput, type UserListInput } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

type UserListQuery = Partial<UserListInput>

type ColleagueListQuery = Partial<ColleagueListInput>

/** 修改当前用户主动设置的工作状态。 */
export const updateUserWorkStatus = ops.updateUserWorkStatus

/** 读取企业成员详情。 */
export const getUser = ops.getUser

/** 修改企业成员头像、资料、角色、接待设置和所属团队。 */
export const updateUser = ops.updateUser

/** 禁用企业成员账号。 */
export const deactivateUser = ops.deactivateUser

/** 将企业成员账号恢复为正常状态。 */
export const reactivateUser = ops.reactivateUser

/** 读取企业成员列表。 */
export function listUsers(query: UserListQuery, signal?: AbortSignal) {
  return ops.listUsers(
    {
      query: query.query ?? "",
      status: query.status,
      roleId: query.roleId ?? "",
      teamId: query.teamId ?? "",
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 分页读取企业全部在职成员，供负责人等选择项使用。 */
export async function listAllActiveUsers() {
  const pageSize = 100
  const query = { status: UserStatus.Active, pageSize }
  const output = await listUsers({ ...query, page: 1 })
  const users = [...output.users]
  for (let page = 2; page <= Math.ceil(output.page.total / pageSize); page += 1) {
    users.push(...(await listUsers({ ...query, page })).users)
  }
  return users
}

/** 读取通讯录同事目录，服务台排在成员之前。 */
export function listColleagues(query: ColleagueListQuery, signal?: AbortSignal) {
  return ops.listColleagues(
    {
      query: query.query ?? "",
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}
