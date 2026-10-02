/** 收件箱会话选择器共用的企业身份候选读取。 */
import {
  listPersonalAgents,
  listMemberOptions,
  OrganizationIdentityType,
  UserStatus,
  type MemberOption,
} from "@/api"

const memberOptionPageSize = 100

/** 分页读取全部可用企业身份候选项。 */
export async function listAllMemberOptions() {
  const members: MemberOption[] = []
  let page = 1
  let pages = 1
  do {
    const output = await listMemberOptions({
      page,
      pageSize: memberOptionPageSize,
    })
    members.push(...output.members)
    pages = Math.ceil(output.page.total / memberOptionPageSize)
    page += 1
  } while (page <= pages)
  return members
}

/** 单聊与群聊的候选对象，personal 表示当前成员负责的个人 AI 员工。 */
export type ChatTarget = MemberOption & { personal?: boolean }

/** 读取可发起单聊或加入群聊的对象：全部企业身份候选项与本人负责的正常个人 AI 员工。 */
export async function listChatTargets(): Promise<ChatTarget[]> {
  const [members, owned] = await Promise.all([listAllMemberOptions(), listPersonalAgents()])
  const personalAgents: ChatTarget[] = owned.personalAgents
    .filter((agent) => agent.status === UserStatus.UserStatusActive)
    .map((agent) => ({
      id: agent.identityId,
      type: OrganizationIdentityType.OrganizationIdentityTypeAgent,
      displayName: agent.displayName,
      avatarUrl: agent.avatarUrl,
      personal: true,
    }))
  return [...members, ...personalAgents]
}

/** 按姓名筛选单聊对象，忽略首尾空白与大小写。 */
export function filterChatTargets(members: ChatTarget[], search: string) {
  const keyword = search.trim().toLocaleLowerCase()
  return members.filter((member) => member.displayName.toLocaleLowerCase().includes(keyword))
}

/** 排除本人后按同事、AI 员工、个人 AI 员工的顺序排列单聊对象。 */
export function orderChatTargets(members: ChatTarget[], currentIdentityId: string) {
  const others = members.filter((member) => member.id !== currentIdentityId)
  return [
    others.filter((member) => member.type === OrganizationIdentityType.OrganizationIdentityTypeUser),
    others.filter((member) => member.type === OrganizationIdentityType.OrganizationIdentityTypeAgent && !member.personal),
    others.filter((member) => member.personal),
  ].flat()
}
