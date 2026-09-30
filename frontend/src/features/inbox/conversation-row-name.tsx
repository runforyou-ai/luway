/** 会话列表项的名称，AI 员工会话的员工名称限宽截断。 */
import { isAgentInboxConversation, type InboxConversationData } from "@/api"
import { cn } from "@/lib/utils"

/** 渲染列表项名称；AI 员工会话的员工名称最多占四成宽度并单独截断，会话标题占剩余宽度，悬停显示完整名称。 */
export function ConversationRowName({
  conversation,
  name,
  className,
}: {
  conversation: InboxConversationData
  name: string
  className?: string
}) {
  if (!isAgentInboxConversation(conversation)) {
    return <span className={cn("min-w-0 flex-1 truncate", className)}>{name}</span>
  }
  return (
    <span title={name} className={cn("flex min-w-0 flex-1 gap-1", className)}>
      <span className="max-w-[40%] shrink-0 truncate">{conversation.agent.agentName}</span>
      <span className="shrink-0">·</span>
      <span className="min-w-0 truncate">{conversation.agent.title}</span>
    </span>
  )
}
