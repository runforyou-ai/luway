/** 企业团队与成员分配调用。 */
import {
  AddTeamMembers,
  CreateTeam,
  DeleteTeam,
  GetTeam,
  ListMemberOptions,
  ListTeamMemberCandidates,
  ListTeamMembers,
  ListTeams,
  RemoveTeamMembers,
  UpdateTeam,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import type {
  MemberOptionListInput,
  TeamListInput,
  TeamMemberCandidateInput,
  TeamMemberListInput,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import { bind } from "@/api/client"

type TeamListQuery = Partial<TeamListInput>

type TeamMemberCandidateQuery = Partial<TeamMemberCandidateInput>

type TeamMemberListQuery = Partial<TeamMemberListInput>

type MemberOptionListQuery = Partial<MemberOptionListInput>

const listTeamsBound = bind(ListTeams)
const listMemberOptionsBound = bind(ListMemberOptions)
const listTeamMemberCandidatesBound = bind(ListTeamMemberCandidates)
const listTeamMembersBound = bind(ListTeamMembers)

/** 创建企业团队。 */
export const createTeam = bind(CreateTeam)

/** 修改企业团队。 */
export const updateTeam = bind(UpdateTeam)

/** 读取企业团队详情。 */
export const getTeam = bind(GetTeam)

/** 删除企业团队。 */
export const deleteTeam = bind(DeleteTeam)

/** 将企业身份批量加入团队。 */
export const addTeamMembers = bind(AddTeamMembers)

/** 将企业身份批量移出团队。 */
export const removeTeamMembers = bind(RemoveTeamMembers)

/** 读取尚未加入团队的企业成员。 */
export function listTeamMemberCandidates(
  teamId: string,
  query: TeamMemberCandidateQuery = {},
  signal?: AbortSignal,
) {
  return listTeamMemberCandidatesBound(
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
  return listTeamMembersBound(
    teamId,
    {
      query: query.query ?? "",
      workStatus: query.workStatus ?? null,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}

/** 读取企业团队列表。 */
export function listTeams(query: TeamListQuery = {}, signal?: AbortSignal) {
  return listTeamsBound(
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

/** 读取可分配的企业成员和 AI 员工。 */
export function listMemberOptions(
  query: MemberOptionListQuery = {},
  signal?: AbortSignal,
) {
  return listMemberOptionsBound(
    {
      query: query.query ?? "",
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  )
}
