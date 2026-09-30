/** 移动端消息：群聊、单聊与 AI 会话列表、会话类型筛选和新聊天入口。 */
import { useState } from "react"
import { PlusIcon, SearchIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate, useSearchParams } from "react-router"

import { InboxScope, type ConversationType } from "@/api"
import {
  MobileConversationList,
  MobileListHeader,
  useMobileListOptions,
} from "@/apps/mobile/mobile-conversation-list"
import { MobileFilterSheet } from "@/apps/mobile/mobile-filter-sheet"
import { mobileSearchPath } from "@/apps/mobile/mobile-navigation"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  chatKindOptions,
  inboxQueryFromSearch,
  toggleChatKinds,
  type NormalizedInboxQuery,
} from "@/features/inbox/inbox-query"
import { usePartitionedInboxList } from "@/features/inbox/use-inbox-list"
import { useInboxListViewport } from "@/features/inbox/use-inbox-list-viewport"

/** 从地址读取会话类型筛选，按筛选挂载独立列表窗口。 */
export function MobileChatsPage() {
  const [params, setParams] = useSearchParams()
  const query = inboxQueryFromSearch(params, [InboxScope.InboxScopeChat])

  /** 更换会话类型时替换当前列表地址，全部类型不写入地址。 */
  function changeKinds(kinds: ConversationType[]) {
    setParams(kinds.length ? { kinds: kinds.join(",") } : {}, { replace: true })
  }
  return <MobileChatList key={query.kinds.join(",")} query={query} onKindsChange={changeKinds} />
}

/** 渲染消息标题、检索与新聊天入口、类型筛选和置顶在前的会话列表。 */
function MobileChatList({
  query,
  onKindsChange,
}: {
  query: NormalizedInboxQuery
  onKindsChange: (kinds: ConversationType[]) => void
}) {
  const { t } = useTranslation(["mobile", "inbox", "common"])
  const navigate = useNavigate()
  const viewport = useInboxListViewport()
  const list = usePartitionedInboxList(query, viewport, useMobileListOptions())
  const [kinds, setKinds] = useState<ConversationType[]>(query.kinds)
  const [sorting, setSorting] = useState(false)
  const summary = query.kinds.length
    ? chatKindOptions
        .filter((option) => query.kinds.includes(option.kind))
        .map((option) => t(`inbox:${option.label}`))
        .join("、")
    : t("inbox:filterAllKinds")
  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobileListHeader
        title={t("chats.title")}
        sorting={sorting}
        onSortingDone={() => setSorting(false)}
        actions={
          <>
            <Button
              variant="ghost"
              size="icon-lg"
              className="shrink-0"
              aria-label={t("common:actions.search")}
              onClick={() => navigate(mobileSearchPath(), { state: { mobileBack: true } })}
            >
              <SearchIcon />
            </Button>
            <DropdownMenu onOpenChange={viewport.setMenu}>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-lg"
                  className="shrink-0"
                  aria-label={t("chats.add")}
                >
                  <PlusIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  className="min-h-11"
                  onSelect={() => navigate("/chats/group/new", { state: { mobileBack: true } })}
                >
                  {t("group.create")}
                </DropdownMenuItem>
                <DropdownMenuItem
                  className="min-h-11"
                  onSelect={() => navigate("/chats/new", { state: { mobileBack: true } })}
                >
                  {t("inbox:newDirectConversation")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />
      <MobileFilterSheet
        summary={summary}
        onOpen={() => setKinds(query.kinds)}
        onApply={() => onKindsChange(kinds)}
        onOpenChange={viewport.setMenu}
      >
        <fieldset className="space-y-2">
          <legend className="pb-2 text-sm font-medium">{t("inbox:filterKind")}</legend>
          {chatKindOptions.map((option) => (
            <label key={option.kind} className="flex min-h-11 items-center gap-3 text-sm">
              <input
                type="checkbox"
                className="size-4 accent-primary"
                checked={kinds.includes(option.kind)}
                onChange={(event) =>
                  setKinds(toggleChatKinds(kinds, option.kind, event.target.checked))
                }
              />
              <span>{t(`inbox:${option.label}`)}</span>
            </label>
          ))}
        </fieldset>
      </MobileFilterSheet>
      <MobileConversationList
        list={list}
        viewport={viewport}
        showAssignee={false}
        showAudience={false}
        emptyTitle={t("chats.emptyTitle")}
        emptyDescription={t("chats.emptyDescription")}
        sorting={sorting}
        onSortingChange={setSorting}
      />
    </section>
  )
}
