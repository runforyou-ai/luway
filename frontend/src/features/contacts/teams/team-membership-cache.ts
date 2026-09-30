/** 团队或成员关系变化后需要失效的缓存。 */
import { resourceKeys } from "@/hooks/resource-keys"

/** 团队详情，以及内嵌所属团队信息的成员、AI 员工、同事目录与服务队列缓存。 */
export const teamMembershipCacheKeys = [
  resourceKeys.team(),
  resourceKeys.serviceQueueTeams(),
  resourceKeys.users(),
  resourceKeys.user(),
  resourceKeys.agents(),
  resourceKeys.agent(),
  resourceKeys.colleagues(),
]
