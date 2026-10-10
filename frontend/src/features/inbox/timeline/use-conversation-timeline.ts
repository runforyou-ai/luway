/** 管理单个连续消息窗口、资源读取和最新/锚点浏览意图。 */
import { useId, useMemo, useRef, useSyncExternalStore, type RefObject } from "react"
import {
  getConversationMessageContext,
  listConversationMessages,
  readConversationMessageWindow,
} from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceReader } from "@/hooks/use-resource"
import { ConversationWindowController } from "./conversation-window"

/** 由资源失效驱动当前窗口重读。 */
export function useConversationTimeline({
  conversationID,
  enabled,
  viewport,
}: {
  conversationID: string
  enabled: boolean
  viewport: RefObject<{ keepPosition: () => void; followingLatest: () => boolean } | null>
}) {
  const read = useResourceReader()
  const readRef = useRef(read)
  readRef.current = read
  const view = useId()
  const controller = useMemo(
    () =>
      new ConversationWindowController({
        latest: () =>
          readRef.current(
            resourceKeys.conversationMessagePage(conversationID, { before: "", after: "" }),
            (signal) => listConversationMessages(conversationID, undefined, signal),
          ),
        context: (messageID) =>
          readRef.current(
            resourceKeys.conversationMessageContext(conversationID, messageID),
            (signal) => getConversationMessageContext(conversationID, messageID, signal),
          ),
        page: (direction, cursor) => {
          const parameters = {
            before: direction === "before" ? cursor : "",
            after: direction === "after" ? cursor : "",
          }
          return readRef.current(
            resourceKeys.conversationMessagePage(conversationID, parameters),
            (signal) => listConversationMessages(conversationID, parameters, signal),
          )
        },
        window: (start, end) =>
          readRef.current(
            resourceKeys.conversationMessagePage(conversationID, { start, end }),
            (signal) => readConversationMessageWindow(conversationID, { start, end }, signal),
          ),
        keepPosition: () => viewport.current?.keepPosition(),
        followingLatest: () => viewport.current?.followingLatest() ?? false,
      }),
    [conversationID, viewport],
  )
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot)
  // 窗口重读登记为挂载中的查询，同步失效与失败重试共用同一入口。
  const sync = useResource(
    resourceKeys.conversationMessages(conversationID, { view }),
    () => controller.refresh(),
    {
      enabled,
      staleTime: 0,
      refetchOnWindowFocus: false,
    },
  )

  return {
    ...snapshot,
    loading: enabled && !snapshot.page && !sync.error,
    error: snapshot.page ? null : sync.error,
    refreshError: snapshot.page ? sync.error : null,
    refresh: () => sync.refresh({ cancelRefetch: false }),
    openWindow: controller.open,
    loadPage: controller.loadPage,
    cancelWindowUpdate: controller.cancel,
  }
}
