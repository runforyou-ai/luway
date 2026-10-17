/**
 * 消息运行时：为当前登录用户装配发出中消息、草稿与附件队列；按用户编号重建。
 * 工作台级挂载以保留草稿/发出中消息/附件队列跨设置与消息页导航。
 */
import type { ReactNode } from "react"

import {
  AttachmentQueueProvider,
  ComposerDraftProvider,
  OutgoingMessageProvider,
} from "@/features/inbox/shell"

/**
 * 包裹需要发送、草稿与附件队列的页面树。
 * 工作台级挂载以保留草稿/发出中消息/附件队列跨设置与消息页导航。
 */
export function InboxRuntimeProviders({
  userId,
  children,
}: {
  userId: string
  children: ReactNode
}) {
  return (
    <OutgoingMessageProvider key={userId}>
      <ComposerDraftProvider key={userId}>
        <AttachmentQueueProvider key={userId}>{children}</AttachmentQueueProvider>
      </ComposerDraftProvider>
    </OutgoingMessageProvider>
  )
}
