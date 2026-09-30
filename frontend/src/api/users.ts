/** 企业成员账号与通讯录同事目录调用。 */
import {
  DeactivateUser,
  GetUser,
  ListColleagues,
  ListUsers,
  ReactivateUser,
  UpdateUser,
  UpdateUserWorkStatus,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  UserStatus,
  type ColleagueList,
  type ColleagueListInput,
  type User,
  type UserListInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

type UserListQuery = Partial<UserListInput>

export type UserData = NonNullArrays<User>

type ColleagueListQuery = Partial<ColleagueListInput>

type ColleagueListData = NonNullArrays<ColleagueList>

export type ColleagueData = ColleagueListData["colleagues"][number]

const listUsersBound = bind(ListUsers)

const listColleaguesBound = bind(ListColleagues)

/** 修改当前用户主动设置的工作状态。 */
export const updateUserWorkStatus = bind(UpdateUserWorkStatus)

/** 读取企业成员详情。 */
export const getUser = bind(GetUser)


/** 修改企业成员头像、资料、角色、接待设置和所属团队。 */
export const updateUser = bind(UpdateUser)

/** 禁用企业成员账号。 */
export const deactivateUser = bind(DeactivateUser)

/** 将企业成员账号恢复为正常状态。 */
export const reactivateUser = bind(ReactivateUser)

/** 读取企业成员列表。 */
export function listUsers(query: UserListQuery, signal?: AbortSignal) {
  return listUsersBound(
    {
      query: query.query ?? "",
      status: query.status ?? null,
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
  const query = { status: UserStatus.UserStatusActive, pageSize }
  const output = await listUsers({ ...query, page: 1 })
  const users = [...output.users]
  for (let page = 2; page <= Math.ceil(output.page.total / pageSize); page += 1) {
    users.push(...(await listUsers({ ...query, page })).users)
  }
  return users
}

/** 读取通讯录同事目录，服务台排在成员之前。 */
export function listColleagues(query: ColleagueListQuery, signal?: AbortSignal) {
  return listColleaguesBound(
    {
      query: query.query ?? "",
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}
