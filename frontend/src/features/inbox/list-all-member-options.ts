/** 收件箱会话选择器共用的企业身份候选读取。 */
import {
  listAssistants,
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

/** 读取可发起单聊或加入群聊的对象：全部企业身份候选项与本人名下正常的助理。 */
export async function listChatTargets() {
  const [members, owned] = await Promise.all([listAllMemberOptions(), listAssistants()])
  const assistants: MemberOption[] = owned.assistants
    .filter((assistant) => assistant.status === UserStatus.UserStatusActive)
    .map((assistant) => ({
      id: assistant.identityId,
      type: OrganizationIdentityType.OrganizationIdentityTypeAssistant,
      displayName: assistant.displayName,
      avatarUrl: assistant.avatarUrl,
    }))
  return [...members, ...assistants]
}

/** 按姓名筛选单聊对象，忽略首尾空白与大小写。 */
export function filterChatTargets(members: MemberOption[], search: string) {
  const keyword = search.trim().toLocaleLowerCase()
  return members.filter((member) => member.displayName.toLocaleLowerCase().includes(keyword))
}

/** 排除本人后按同事、AI 员工、助理的顺序排列单聊对象。 */
export function orderChatTargets(members: MemberOption[], currentIdentityId: string) {
  const others = members.filter((member) => member.id !== currentIdentityId)
  return [
    OrganizationIdentityType.OrganizationIdentityTypeUser,
    OrganizationIdentityType.OrganizationIdentityTypeAgent,
    OrganizationIdentityType.OrganizationIdentityTypeAssistant,
  ].flatMap((type) => others.filter((member) => member.type === type))
}
