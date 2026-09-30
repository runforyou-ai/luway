/** 集中维护企业成员变更影响的目录、组织关系和候选数据。 */
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 返回真人或 AI 员工变更后需要失效的同事目录、团队、角色、会话候选和知识库使用情况的查询 key。 */
export function contactResourceKeys(kind: "user" | "agent", id?: string) {
  const keys = [
    kind === "user" ? resourceKeys.users() : resourceKeys.agents(),
    resourceKeys.colleagues(),
    resourceKeys.teams(),
    resourceKeys.teamMembers(),
    resourceKeys.teamMemberCandidates(),
    resourceKeys.roles(),
    resourceKeys.roleMembers(),
    resourceKeys.serviceAssignees(),
    resourceKeys.serviceQueueTeams(),
    resourceKeys.memberOptions(),
    resourceKeys.chatTargets(),
  ]
  // AI 员工的名称、状态和知识库绑定变化后刷新知识库的员工列表。
  if (kind === "agent") keys.push(resourceKeys.knowledgeBaseAgents())
  // 成员停用时其名下助理随之停用。
  if (kind === "user") keys.push(resourceKeys.memberAssistants(), resourceKeys.assistants())
  if (id) keys.push(kind === "user" ? resourceKeys.user(id) : resourceKeys.agent(id))
  return keys
}

/** 刷新真人或 AI 员工及其关联数据。 */
export function useContactInvalidator() {
  const invalidate = useResourceInvalidator()
  return (kind: "user" | "agent", id?: string) =>
    Promise.all(contactResourceKeys(kind, id).map((key) => invalidate(key)))
}
