/** 企业团队与成员分配调用。 */
import type {
  MemberOptionListInput,
  TeamListInput,
  TeamMemberCandidateInput,
  TeamMemberListInput,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

type TeamListQuery = Partial<TeamListInput>

type TeamMemberCandidateQuery = Partial<TeamMemberCandidateInput>

type TeamMemberListQuery = Partial<TeamMemberListInput>

type MemberOptionListQuery = Partial<MemberOptionListInput>

/** 创建企业团队。 */
export const createTeam = ops.createTeam

/** 修改企业团队。 */
export const updateTeam = ops.updateTeam

/** 读取企业团队详情。 */
export const getTeam = ops.getTeam

/** 删除企业团队。 */
export const deleteTeam = ops.deleteTeam

/** 将企业身份批量加入团队。 */
export const addTeamMembers = ops.addTeamMembers

/** 将企业身份批量移出团队。 */
export const removeTeamMembers = ops.removeTeamMembers

/** 读取尚未加入团队的企业成员。 */
export function listTeamMemberCandidates(
  teamId: string,
  query: TeamMemberCandidateQuery = {},
  signal?: AbortSignal,
) {
  return ops.listTeamMemberCandidates(
    teamId,
    {
      query: query.query ?? "",
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 读取团队成员列表。 */
export function listTeamMembers(
  teamId: string,
  query: TeamMemberListQuery,
  signal?: AbortSignal,
) {
  return ops.listTeamMembers(
    teamId,
    {
      query: query.query ?? "",
      workStatus: query.workStatus,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 读取企业团队列表。 */
export function listTeams(query: TeamListQuery = {}, signal?: AbortSignal) {
  return ops.listTeams(
    {
      query: query.query ?? "",
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 分页读取企业全部团队，供路由目标等选择项使用。 */
export async function listAllTeams() {
  const pageSize = 100
  const output = await listTeams({ page: 1, pageSize })
  const teams = [...output.teams]
  for (let page = 2; page <= Math.ceil(output.page.total / pageSize); page += 1) {
    teams.push(...(await listTeams({ page, pageSize })).teams)
  }
  return teams
}

/** 分页读取全部可用企业身份候选项。 */
export async function listAllMemberOptions() {
  const pageSize = 100
  const output = await listMemberOptions({ page: 1, pageSize })
  const members = [...output.members]
  for (let page = 2; page <= Math.ceil(output.page.total / pageSize); page += 1) {
    members.push(...(await listMemberOptions({ page, pageSize })).members)
  }
  return members
}

/** 读取可分配的企业成员和 AI 员工。 */
export function listMemberOptions(
  query: MemberOptionListQuery = {},
  signal?: AbortSignal,
) {
  return ops.listMemberOptions(
    {
      query: query.query ?? "",
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}
