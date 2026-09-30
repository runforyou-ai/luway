/** 全局搜索模态：检索输入、结果列表和结果打开。 */
import { SearchIcon, XIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useState } from "react"
import { useLocation, useNavigate } from "react-router"

import { InboxSearchPersonKind, type Identity } from "@/api"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { InboxSearchPanel } from "@/features/inbox/inbox-search-panel"
import { useConversationName } from "@/hooks/use-conversation-name"
import { useConversationSummary } from "@/features/inbox/use-conversation-summary"
import { useInboxSearch, type InboxSearchItem } from "@/features/inbox/use-inbox-search"
import { useRecentConversations } from "@/features/inbox/use-recent-conversations"

/** 按检索范围、结果类型和结果列表渲染全局搜索模态。 */
export function InboxSearchDialog({
  identity,
  conversationId,
  open,
  onOpenChange,
}: {
  identity: Identity
  conversationId: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation(["inbox", "common"])
  const navigate = useNavigate()
  const location = useLocation()
  const recent = useRecentConversations(identity.user.identityId)
  const onInbox = location.pathname === "/inbox"
  // 从会话进入时限定该会话，可随时退出改为检索全部可读消息。
  const [scopedConversationId, setScopedConversationId] = useState(conversationId)
  const search = useInboxSearch({
    conversationId: scopedConversationId,
    recentConversationIds: recent.ids,
    onOpen: openItem,
  })
  const scopedConversation = useConversationSummary(scopedConversationId)
  const conversationName = useConversationName()
  const scopedName = scopedConversation.data ? conversationName(scopedConversation.data) : ""

  /** 打开搜索结果：服务会话在收件箱打开，聊天在聊天页打开；消息定位到原消息，成员和 AI 按身份进入聊天。 */
  function openItem(item: InboxSearchItem) {
    const conversation =
      item.kind === "message" ? item.message.conversation : item.kind === "conversation" ? item.conversation : null
    let conversationId = conversation?.id ?? ""
    let service = Boolean(conversation?.service)
    if (item.kind === "person") {
      if (item.person.kind !== InboxSearchPersonKind.InboxSearchPersonContact) {
        onOpenChange(false)
        navigate(`/chats?target=${item.person.id}`)
        return
      }
      if (!item.person.conversationId) return
      conversationId = item.person.conversationId
      service = true
    }
    // 已在收件箱时保留当前页签与筛选，其余入口按默认筛选打开。
    const params = new URLSearchParams(service && onInbox ? location.search : "")
    params.delete("message")
    params.set("conversation", conversationId)
    if (item.kind === "message") params.set("message", item.message.id)
    onOpenChange(false)
    navigate(`${service ? "/inbox" : "/chats"}?${params.toString()}`)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="top-[12vh] flex h-[min(34rem,72svh)] max-w-2xl translate-y-0 flex-col gap-0 overflow-hidden p-0"
        closeButtonClassName="hidden"
      >
        <DialogHeader className="sr-only">
          <DialogTitle>{t("common:actions.search")}</DialogTitle>
          <DialogDescription>{t("searchDescription")}</DialogDescription>
        </DialogHeader>
        <div className="shrink-0 border-b">
          <div className="flex items-center gap-2.5 px-4">
            <SearchIcon className="size-4 shrink-0 text-muted-foreground" />
            <input
              type="text"
              value={search.text}
              aria-label={t("common:actions.search")}
              className="h-12 min-w-0 flex-1 bg-transparent text-sm outline-none"
              onChange={(event) => search.setText(event.target.value)}
              onKeyDown={search.handleKeyDown}
            />
          </div>
        </div>
        {scopedConversationId ? (
          <div className="flex h-7 shrink-0 items-center gap-2 bg-muted px-4 text-xs text-muted-foreground">
            <span className="min-w-0 flex-1 truncate">
              {scopedName
                ? t("searchConversationScope", { name: scopedName })
                : t("searchCurrentConversation")}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              className="size-5 text-muted-foreground"
              aria-label={t("searchConversationScopeClear")}
              title={t("searchConversationScopeClear")}
              onClick={() => setScopedConversationId("")}
            >
              <XIcon />
            </Button>
          </div>
        ) : null}
        <InboxSearchPanel search={search} identity={identity} />
      </DialogContent>
    </Dialog>
  )
}
