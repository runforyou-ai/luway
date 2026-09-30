/** 移动端个人中心的已归档聊天：按名称搜索、按类型筛选，打开或取消归档。 */
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Link, useLocation } from "react-router"

import { ConversationType, listArchivedConversations } from "@/api"
import { MobileFilterSheet } from "@/apps/mobile/mobile-filter-sheet"
import { mobileConversationPath, useMobileNavigation } from "@/apps/mobile/mobile-navigation"
import { MobilePageHeader, MobileSearchBar } from "@/apps/mobile/mobile-page"
import { MobilePagedList } from "@/apps/mobile/mobile-paged-list"
import { Button } from "@/components/ui/button"
import { ConversationAvatar } from "@/features/inbox/conversation-avatar"
import { conversationPreview } from "@/features/inbox/conversation-preview"
import { useConversationArchive } from "@/features/inbox/use-conversation-archive"
import { useConversationName } from "@/hooks/use-conversation-name"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 类型筛选的可选项与文案，空值表示全部类型。 */
const kindFilters = [
  { value: ConversationType.$zero, label: "filterAllKinds" },
  { value: ConversationType.ConversationTypeGroup, label: "filterKindGroup" },
  { value: ConversationType.ConversationTypeDirect, label: "filterKindDirect" },
  { value: ConversationType.ConversationTypeAgent, label: "filterKindAgent" },
] as const

/** 防抖同步名称搜索并按类型筛选已归档的聊天，切换条件时重置目标查询的加载进度。 */
export function MobileArchivedChatsPage() {
  const { t } = useTranslation(["settings", "inbox"])
  const location = useLocation()
  const { listPageCounts, scrollPositions } = useMobileNavigation()
  const { searchParams, setParameters, query, search, setSearch } = useListSearchParams({
    onQueryChange: (next) => {
      const storageKey = `archived-chats:${kind}:${next}`
      listPageCounts.delete(storageKey)
      scrollPositions.delete(storageKey)
    },
  })
  const kind = optionalWailsEnum(ConversationType, searchParams.get("kind")) ?? ConversationType.$zero
  const [draftKind, setDraftKind] = useState<ConversationType>(ConversationType.$zero)
  const kindLabel = (value: ConversationType) =>
    t(`inbox:${kindFilters.find((option) => option.value === value)?.label ?? "filterAllKinds"}`)

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("archivedChats.title")} backTo="/me" />
      <MobileSearchBar label={t("archivedChats.search")} value={search} onChange={setSearch} />
      <MobileFilterSheet
        summary={kind ? kindLabel(kind) : ""}
        onOpen={() => setDraftKind(kind)}
        onReset={() => setDraftKind(ConversationType.$zero)}
        onApply={() => {
          const storageKey = `archived-chats:${draftKind}:${query.trim()}`
          listPageCounts.delete(storageKey)
          scrollPositions.delete(storageKey)
          setParameters({ kind: draftKind || null }, true, location.state)
        }}
      >
        <div role="group" aria-label={t("inbox:filterKind")} className="grid grid-cols-2 gap-2">
          {kindFilters.map((option) => (
            <Button
              key={option.value}
              variant={draftKind === option.value ? "default" : "outline"}
              className="min-h-11"
              aria-pressed={draftKind === option.value}
              onClick={() => setDraftKind(option.value)}
            >
              {kindLabel(option.value)}
            </Button>
          ))}
        </div>
      </MobileFilterSheet>
      <MobileArchivedChatList
        key={`archived-chats:${kind}:${query.trim()}`}
        kind={kind}
        queryText={query.trim()}
        searching={search !== query}
      />
    </section>
  )
}

/** 逐页读取已归档的聊天，点击打开聊天，行尾按钮取消归档。 */
function MobileArchivedChatList({
  kind,
  queryText,
  searching,
}: {
  kind: ConversationType
  queryText: string
  searching: boolean
}) {
  const { t } = useTranslation(["settings", "inbox", "mobile"])
  const { t: tInbox } = useTranslation("inbox")
  const { formatDateTime } = useDateTime()
  const conversationName = useConversationName()
  const archive = useConversationArchive()
  return (
    <MobilePagedList
      storageKey={`archived-chats:${kind}:${queryText}`}
      searching={searching}
      labels={{
        loadError: t("archivedChats.loadError"),
        empty: t("archivedChats.empty"),
        allLoaded: t("mobile:archivedChats.allLoaded"),
      }}
      source={(page) => {
        const input = { search: queryText, kind, page, pageSize: 50 }
        return {
          key: resourceKeys.archivedConversations({ query: queryText, kind, page, pageSize: 50 }),
          load: (signal) => listArchivedConversations(input, signal),
        }
      }}
      select={(data) => ({ items: data.conversations, page: data.page })}
    >
      {(conversations) => (
        <ul className="divide-y border-b">
          {conversations.map((conversation) => (
            <li key={conversation.id} className="flex min-h-18 items-center gap-3 px-4 py-3">
              <Link
                to={mobileConversationPath(conversation)}
                state={{ conversation, mobileBack: true }}
                className="flex min-w-0 flex-1 items-center gap-3 outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <ConversationAvatar conversation={conversation} className="size-10 shrink-0" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[15px] font-medium">
                    {conversationName(conversation)}
                  </span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {conversationPreview(conversation, tInbox)}
                  </span>
                  {conversation.archivedAt ? (
                    <span className="block truncate text-xs text-muted-foreground">
                      {t("archivedChats.archivedAt", { time: formatDateTime(conversation.archivedAt) })}
                    </span>
                  ) : null}
                </span>
              </Link>
              <Button
                type="button"
                variant="outline"
                className="min-h-11 shrink-0"
                disabled={archive.isSaving(conversation.id)}
                onClick={() => void archive.save(conversation.id, false)}
              >
                {tInbox("conversationUnarchive")}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </MobilePagedList>
  )
}
