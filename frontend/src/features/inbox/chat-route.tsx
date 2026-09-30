/** 聊天路由：打开一级栏中的群聊、单聊或新聊天草稿，会话占满主区。 */
import { useEffect, useRef, useState, type ReactNode } from "react"
import { MessagesSquareIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useSearchParams } from "react-router"

import {
  type AgentInboxConversationData,
  type DirectInboxConversationData,
  type MemberOption,
} from "@/api"
import { LoadingIndicator } from "@/components/loading-indicator"
import { useGlobalSearch } from "@/contexts/global-search-context"
import { useWorkspace } from "@/contexts/workspace-context"
import { ConversationDetail } from "@/features/inbox/conversation-detail"
import type { ConversationLocateTarget } from "@/features/inbox/conversation-timeline"
import { InboxConversationTarget } from "@/features/inbox/inbox-conversation-target"
import type { ChatDraft } from "@/features/inbox/inbox-selection"
import { LazyConversationMain } from "@/features/inbox/lazy-conversation-main"
import {
  readConversationSummary,
  useConversationSummary,
} from "@/features/inbox/use-conversation-summary"
import { useOpenedConversation } from "@/features/inbox/use-opened-conversation"
import { resourceKeys } from "@/hooks/resource-keys"
import { useIsNarrowViewport } from "@/hooks/use-narrow-viewport"
import { useResourceInvalidator, useResourceReader } from "@/hooks/use-resource"
import { isAIIdentityType } from "@/lib/identity-type"

/** 按地址中的会话、聊天对象或定位消息渲染聊天主区。 */
export function ChatRoute() {
  const { t } = useTranslation("inbox")
  const { identity } = useWorkspace()
  const [searchParams, setSearchParams] = useSearchParams()
  const conversationId = searchParams.get("conversation") ?? ""
  const targetIdentityId = searchParams.get("target") ?? ""
  const locateMessageId = searchParams.get("message") ?? ""
  const narrowViewport = useIsNarrowViewport()
  const globalSearch = useGlobalSearch()
  const invalidate = useResourceInvalidator()
  const readResource = useResourceReader()
  const [draft, setDraft] = useState<ChatDraft | null>(null)
  const [locateMessage, setLocateMessage] = useState<ConversationLocateTarget | null>(null)
  const locateNonce = useRef(0)
  const navigationGeneration = useRef(0)
  const summary = useConversationSummary(targetIdentityId ? "" : conversationId)
  const openedConversationId = summary.data?.id

  useOpenedConversation(identity.user.identityId, openedConversationId)
  useEffect(() => {
    // 地址变化即切换聊天：清除上一会话的消息定位，未完成的新建流程停止回写地址；同一地址带来的新定位由下方随后设置。
    navigationGeneration.current++
    setLocateMessage(null)
    if (conversationId || targetIdentityId) setDraft(null)
  }, [conversationId, targetIdentityId])
  useEffect(() => {
    // 搜索结果带来的消息定位一次性生效，随后从地址中移除。
    if (!locateMessageId) return
    locateNonce.current++
    setLocateMessage({ messageId: locateMessageId, nonce: locateNonce.current })
    setSearchParams((current) => {
      const next = new URLSearchParams(current)
      next.delete("message")
      return next
    }, { replace: true })
  }, [locateMessageId, setSearchParams])

  /** 打开正式会话并替换当前地址。 */
  function openConversation(id: string, replace: boolean) {
    setLocateMessage(null)
    setSearchParams(id ? { conversation: id } : {}, { replace })
  }

  /** 新聊天发出首条消息后先读取权威摘要，再把草稿切换为正式会话。 */
  async function showStartedConversation(conversation: DirectInboxConversationData | AgentInboxConversationData) {
    const generation = navigationGeneration.current
    try {
      await readResource(resourceKeys.conversationSummary(conversation.id), (signal) => readConversationSummary(conversation.id, signal))
    } catch (error) {
      console.warn("读取新建会话摘要失败", { conversationId: conversation.id, error })
    }
    void invalidate(resourceKeys.inbox())
    if (generation !== navigationGeneration.current) return
    setDraft(null)
    openConversation(conversation.id, true)
  }

  /** 真人复用已有单聊，其余在主区开始聊天草稿。 */
  function showTarget(member: MemberOption, existing: DirectInboxConversationData | null) {
    if (existing) {
      openConversation(existing.id, true)
      return
    }
    setDraft(
      isAIIdentityType(member.type)
        ? { kind: "agent-draft", member, conversationId: crypto.randomUUID() }
        : { kind: "direct-draft", member },
    )
    setSearchParams({}, { replace: true })
  }

  let content: ReactNode = null
  if (targetIdentityId) {
    content = (
      <LoadingIndicator className="flex-1 justify-center">
        {t("chatTargetLoading")}
      </LoadingIndicator>
    )
  } else if (draft) {
    content = (
      <LazyConversationMain
        selection={draft}
        onChatStarted={showStartedConversation}
        onSearchConversation={(id) => globalSearch?.open(id)}
        locateMessage={null}
        narrowViewport={narrowViewport}
      />
    )
  } else if (conversationId) {
    content = (
      <ConversationDetail
        summary={summary}
        onGroupLeft={() => openConversation("", true)}
        onSearchConversation={(id) => globalSearch?.open(id)}
        locateMessage={locateMessage}
        narrowViewport={narrowViewport}
      />
    )
  }

  return (
    <>
      {content ? (
        <section className="flex min-h-0 flex-1 flex-col">{content}</section>
      ) : (
        <div className="app-inbox-empty-main flex min-h-0 flex-1 items-center justify-center p-6">
          <div data-slot="empty-state-content" className="max-w-sm text-center">
            <div className="mx-auto mb-4 flex size-11 items-center justify-center rounded-xl border bg-background shadow-sm">
              <MessagesSquareIcon className="size-5 text-muted-foreground" />
            </div>
            <h2 className="text-base font-semibold tracking-tight">{t("chatEmptyTitle")}</h2>
            <p className="mt-2 text-sm text-muted-foreground">{t("chatEmptyDescription")}</p>
          </div>
        </div>
      )}
      {targetIdentityId ? (
        <InboxConversationTarget
          key={targetIdentityId}
          identityId={targetIdentityId}
          currentIdentityId={identity.user.identityId}
          onSelected={showTarget}
          onFailed={() => openConversation("", true)}
        />
      ) : null}
    </>
  )
}
