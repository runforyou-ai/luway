/** 按会话串行提交个人未读标记，保持进入会话和菜单操作的先后顺序。 */
import { subscribeSessionGeneration } from "./session-scope.ts"

const pendingWrites = new Map<string, Promise<void>>()

// 进入新的登录会话时清空排队链，新会话的写入从空队列开始。
subscribeSessionGeneration(() => pendingWrites.clear())

/** 等待同一会话的前次写入结束，再提交本次未读标记变更。 */
export function enqueueConversationUnreadChange<T>(
  conversationID: string,
  write: () => Promise<T>,
): Promise<T> {
  const result = (pendingWrites.get(conversationID) ?? Promise.resolve()).then(write)
  // 失败交给原调用方处理，后续操作仍可继续提交。
  const settled = result.then(() => {}, () => {})
  pendingWrites.set(conversationID, settled)
  void settled.then(() => {
    if (pendingWrites.get(conversationID) === settled) {
      pendingWrites.delete(conversationID)
    }
  })
  return result
}
