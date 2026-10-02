/** 个人 AI 员工变更后需要失效的查询。 */
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 返回个人 AI 员工变更后需要失效的 AI 员工目录、本人与成员负责的个人 AI 员工、详情和聊天对象候选的查询 key。 */
export function personalAgentResourceKeys(id?: string) {
  const keys = [
    resourceKeys.agents(),
    resourceKeys.personalAgents(),
    resourceKeys.memberPersonalAgents(),
    resourceKeys.chatTargets(),
  ]
  if (id) keys.push(resourceKeys.personalAgent(id))
  return keys
}

/** 刷新个人 AI 员工及其关联数据。 */
export function usePersonalAgentInvalidator() {
  const invalidate = useResourceInvalidator()
  return (id?: string) => Promise.all(personalAgentResourceKeys(id).map((key) => invalidate(key)))
}
