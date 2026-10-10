/** 将附件上传队列保持在整个已登录工作台的生命周期内。 */
import {
  createContext,
  useContext,
  useEffect,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react"
import { useTranslation } from "react-i18next"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { resourceKeys } from "@/hooks/resource-keys"
import { AttachmentQueue } from "@/features/inbox/state/attachment-queue"
import { useOutgoingMessageStore } from "@/features/inbox/state/outgoing-message-context"

const AttachmentQueueContext = createContext<AttachmentQueue | null>(null)

/** 为所有聊天页面提供同一上传队列。 */
export function AttachmentQueueProvider({ children }: { children: ReactNode }) {
  const invalidate = useResourceInvalidator()
  const reportError = useRequestErrorReporter()
  const { t } = useTranslation("inbox")
  const outgoing = useOutgoingMessageStore()
  const [queue] = useState(
    () =>
      new AttachmentQueue(
        outgoing,
        (conversationID) => {
          void invalidate(resourceKeys.conversationMessages(conversationID))
          void invalidate(resourceKeys.inbox())
        },
        (error) => {
          reportError(error, { fallback: t("messageSendError") })
        },
      ),
  )
  useEffect(() => {
    queue.start()
    // 页面进入往返缓存时保留队列，真正卸载时才结束。
    const leave = (event: PageTransitionEvent) => {
      if (!event.persisted) queue.dispose()
    }
    window.addEventListener("pagehide", leave)
    return () => {
      window.removeEventListener("pagehide", leave)
      queue.dispose()
    }
  }, [queue])
  return (
    <AttachmentQueueContext value={queue}>{children}</AttachmentQueueContext>
  )
}

/** 读取当前工作台的附件队列。 */
export function useAttachmentQueue() {
  return useContext(AttachmentQueueContext)
}

/** 订阅与消息或附件对应的上传任务状态，其他任务变化时不重渲染。 */
export function useAttachmentJob(messageID: string, attachmentID: string) {
  const queue = useContext(AttachmentQueueContext)
  return useSyncExternalStore(queue?.subscribe ?? emptySubscribe, () =>
    queue?.find(messageID, attachmentID),
  )
}
/** 未提供附件队列时返回空订阅。 */
function emptySubscribe() {
  return () => {}
}
