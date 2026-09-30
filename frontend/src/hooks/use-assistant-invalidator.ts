/** 助理变更后需要失效的查询。 */
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 返回助理变更后需要失效的列表、详情、成员名下助理和聊天对象候选的查询 key。 */
export function assistantResourceKeys(id?: string) {
  const keys = [resourceKeys.assistants(), resourceKeys.memberAssistants(), resourceKeys.chatTargets()]
  if (id) keys.push(resourceKeys.assistant(id))
  return keys
}

/** 刷新助理及其关联数据。 */
export function useAssistantInvalidator() {
  const invalidate = useResourceInvalidator()
  return (id?: string) => Promise.all(assistantResourceKeys(id).map((key) => invalidate(key)))
}
