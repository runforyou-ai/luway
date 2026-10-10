/** 会话详情：按摘要读取结果渲染加载中、不可用、读取失败或会话主区，并统一处理消息后刷新与退群清理。 */
import type { InboxConversation } from "@/api"
import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { useAttachmentQueue } from "@/features/inbox/state/attachment-queue-context"
import { clearConversationResources } from "@/features/inbox/shared/conversation-resources"
import type { ConversationLocateTarget } from "@/features/inbox/timeline/conversation-timeline"
import { LazyConversationMain } from "@/features/inbox/conversation/lazy-conversation-main"
import { useOutgoingMessageStore } from "@/features/inbox/state/outgoing-message-context"
import type { ConversationSummaryResource } from "@/features/inbox/shared/use-conversation-summary"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 渲染指定会话的详情；退群清理完成后由宿主通过 onGroupLeft 决定去向，本人发送消息或处理客服会话并重读摘要后通过 onLocalChange 交给宿主。 */
export function ConversationDetail({
  summary,
  onGroupLeft,
  onLocalChange,
  onSearchConversation,
  locateMessage = null,
  narrowViewport = false,
}: {
  summary: ConversationSummaryResource
  onGroupLeft: (conversationID: string) => void
  onLocalChange?: (conversation: InboxConversation) => void
  onSearchConversation?: (conversationID: string) => void
  locateMessage?: ConversationLocateTarget | null
  narrowViewport?: boolean
}) {
  const { t } = useTranslation(["inbox", "common"])
  const invalidate = useResourceInvalidator()
  const queryClient = useQueryClient()
  const queue = useAttachmentQueue()
  const outgoingStore = useOutgoingMessageStore()
  const conversation = summary.data ?? null

  /** 消息或客服处理保存后刷新列表与详情。 */
  function refreshConversation(conversationID: string) {
    void invalidate(resourceKeys.inbox())
    void invalidate(resourceKeys.conversationSummary(conversationID)).then(() => {
      const current = queryClient.getQueryData<InboxConversation | null>(resourceKeys.conversationSummary(conversationID))
      if (current) onLocalChange?.(current)
    })
  }

  /** 主动退群后清理该会话的本地资源，再交给宿主处理去向。 */
  function cleanUpAfterGroupLeft(conversationID: string) {
    queue?.forgetConversation(conversationID)
    outgoingStore.forgetConversation(conversationID)
    clearConversationResources(queryClient, conversationID)
    void queryClient.resetQueries({ queryKey: resourceKeys.conversationSummary(conversationID) })
    void invalidate(resourceKeys.inbox())
    onGroupLeft(conversationID)
  }

  if (!conversation) {
    return (
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 p-6 text-sm text-muted-foreground">
        {summary.loading ? (
          <LoadingIndicator>{t("messagesLoading")}</LoadingIndicator>
        ) : (
          <>
            <p>{t(summary.data === null ? "conversationUnavailable" : "conversationLoadError")}</p>
            {summary.data !== null ? (
              <Button variant="outline" size="sm" onClick={() => void summary.refresh()}>
                {t("common:actions.retry")}
              </Button>
            ) : null}
          </>
        )}
      </div>
    )
  }

  return (
    <LazyConversationMain
      selection={{ kind: "conversation", conversation }}
      onSessionChanged={refreshConversation}
      onConversationChanged={refreshConversation}
      onGroupLeft={cleanUpAfterGroupLeft}
      onSearchConversation={onSearchConversation}
      locateMessage={locateMessage}
      narrowViewport={narrowViewport}
    />
  )
}
