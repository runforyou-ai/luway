/** 工作台页面地址：路径相对工作区 basename，查询参数统一经 URLSearchParams 编码，未给出的参数不写入地址。 */
import type { NotificationView } from "@/api/realtime/protocol"

/** 查询参数取值，空值与未给出的参数不写入地址。 */
type SearchValues = Record<string, string | null | undefined>

/** 在 base 查询参数上写入 values 并返回带查询串的地址：取值为空时删除该参数。 */
function withSearch(pathname: string, values: SearchValues, base?: string | URLSearchParams) {
  const params = new URLSearchParams(base)
  for (const [name, value] of Object.entries(values)) {
    if (value) params.set(name, value)
    else params.delete(name)
  }
  const search = params.toString()
  return search ? `${pathname}?${search}` : pathname
}

/** 聊天页地址：conversation 打开会话并可用 message 定位消息，target 按成员或 AI 员工的身份打开单聊。 */
export function chatPath(values: { conversation?: string; message?: string; target?: string } = {}) {
  return withSearch("/chats", values)
}

/** 收件箱地址：conversation 打开服务会话并可用 message 定位消息；给出 base 时在其查询参数（页签与筛选）上改写会话与消息。 */
export function inboxPath(values: { conversation?: string; message?: string } = {}, base?: string | URLSearchParams) {
  return withSearch("/inbox", { conversation: values.conversation, message: values.message }, base)
}

/** 待处理页地址，给出 decision 时在侧栏打开该条操作。 */
export function pendingPath(decision?: string) {
  return withSearch("/pending", { decision })
}

/** AI 表现页 AI 员工筛选中表示本人负责的 AI 员工的取值。 */
export const mineAgentFilter = "mine"

/** AI 表现页地址：tab 为页签，agent 为 AI 员工筛选。 */
export function aiPerformancePath(values: { tab?: string; agent?: string } = {}) {
  return withSearch("/ai-performance", values)
}

/** AI 表现页中本人负责的 AI 员工的待处理待补知识。 */
export const responsibleKnowledgeGapsPath = aiPerformancePath({ tab: "knowledgeGaps", agent: mineAgentFilter })

/** 通知的打开视图与会话编号，待处理通知不带会话编号。 */
export type NotificationTarget = {
  view: NotificationView
  conversationId: string
}

/** 工作台中通知对应的页面：待处理通知打开待处理页，服务会话在收件箱，其余在聊天。 */
export function notificationPath(target: NotificationTarget) {
  if (target.view === "pending") return pendingPath()
  return target.view === "service"
    ? inboxPath({ conversation: target.conversationId })
    : chatPath({ conversation: target.conversationId })
}
