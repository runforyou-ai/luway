/** 移动端收件箱检索：与桌面端全局搜索一致的检索范围、分组结果、会话分页列表、最近打开和结果跳转。 */
import { SearchIcon, XIcon } from "lucide-react"
import { useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate, useSearchParams } from "react-router"

import {
  InboxSearchPersonKind,
  InboxSearchRange,
  OrganizationIdentityType,
  type InboxConversationData,
} from "@/api"
import {
  mobileConversationPath,
  useMobileBack,
  useMobileNavigation,
} from "@/apps/mobile/mobile-navigation"
import { MobilePageState, MobileScrollArea } from "@/apps/mobile/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/mobile-workspace-layout"
import { LoadingIndicator } from "@/components/loading-indicator"
import { ProfileAvatar } from "@/components/profile-avatar"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ConversationAvatar } from "@/features/inbox/conversation-avatar"
import { readableInboxQuery } from "@/features/inbox/inbox-query"
import { InboxSearchConversationList } from "@/features/inbox/inbox-search-conversation-list"
import { highlightName, InboxSearchExcerpt } from "@/features/inbox/inbox-search-panel"
import { useContactName } from "@/hooks/use-contact-name"
import { useConversationName } from "@/hooks/use-conversation-name"
import { useConversationSummary } from "@/features/inbox/use-conversation-summary"
import { useConversationTime } from "@/features/inbox/use-conversation-time"
import {
  useInboxSearchResults,
  type InboxSearchPersonData,
  type InboxSearchType,
} from "@/features/inbox/use-inbox-search"
import { useRecentConversations } from "@/features/inbox/use-recent-conversations"
import { isAIIdentityType } from "@/lib/identity-type"

const searchTypes: InboxSearchType[] = ["all", "conversations", "messages", "people"]
const searchGroupLimit = 6

/** 触屏检索结果行，不可打开的结果保留显示并禁用；会话行携带 inboxId 供分页列表滚动补偿定位。 */
function MobileSearchRow({
  inboxId,
  avatar,
  title,
  detail,
  time,
  disabled = false,
  onOpen,
}: {
  inboxId?: string
  avatar: ReactNode
  title: ReactNode
  detail?: ReactNode
  time?: string
  disabled?: boolean
  onOpen: () => void
}) {
  return (
    <li data-inbox-id={inboxId}>
      <button
        type="button"
        disabled={disabled}
        className="flex min-h-14 w-full min-w-0 items-center gap-3 px-4 py-2.5 text-left outline-none select-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:opacity-50"
        onClick={onOpen}
      >
        <span className="shrink-0">{avatar}</span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[15px]">{title}</span>
          {detail ? (
            <span className="mt-0.5 line-clamp-2 text-sm break-words text-muted-foreground">{detail}</span>
          ) : null}
        </span>
        {time ? <span className="shrink-0 self-start pt-0.5 text-xs text-muted-foreground">{time}</span> : null}
      </button>
    </li>
  )
}

/** 检索结果分组，没有结果时不渲染；传入 onViewAll 且结果达到上限时显示查看全部入口。 */
function MobileSearchGroup({
  title,
  count,
  onViewAll,
  children,
}: {
  title: string
  count: number
  onViewAll?: () => void
  children: ReactNode
}) {
  const { t } = useTranslation("inbox")
  if (count === 0) return null
  return (
    <section>
      <h2 className="px-4 pt-4 pb-1 text-xs font-medium text-muted-foreground">{title}</h2>
      <ul>{children}</ul>
      {onViewAll && count >= searchGroupLimit ? (
        <Button type="button" variant="ghost" size="sm" className="ml-2 text-primary" onClick={onViewAll}>
          {t("searchViewAll")}
        </Button>
      ) : null}
    </section>
  )
}

/** 会话结果行，名称按检索词高亮。 */
function MobileConversationRows({
  conversations,
  highlight,
  onOpen,
}: {
  conversations: InboxConversationData[]
  highlight: string
  onOpen: (conversation: InboxConversationData) => void
}) {
  const conversationName = useConversationName()
  return conversations.map((conversation) => (
    <MobileSearchRow
      key={conversation.id}
      inboxId={conversation.id}
      avatar={<ConversationAvatar conversation={conversation} className="size-10" />}
      title={highlightName(conversationName(conversation), highlight)}
      onOpen={() => onOpen(conversation)}
    />
  ))
}

/** 按检索状态渲染最近打开、加载、失败、无结果或分组结果。 */
function MobileSearchResults({
  search,
  query,
  onOpenConversation,
  onOpenPerson,
  onViewAllConversations,
}: {
  search: ReturnType<typeof useInboxSearchResults>
  query: string
  onOpenConversation: (conversation: InboxConversationData, messageId?: string) => void
  onOpenPerson: (person: InboxSearchPersonData) => void
  onViewAllConversations: () => void
}) {
  const { t } = useTranslation("inbox")
  const contactName = useContactName()
  const conversationName = useConversationName()
  const formatTime = useConversationTime()

  if (search.showRecent) {
    return (
      <MobileSearchGroup title={t("searchRecent")} count={search.recentConversations.length}>
        <MobileConversationRows conversations={search.recentConversations} highlight="" onOpen={onOpenConversation} />
      </MobileSearchGroup>
    )
  }
  if (!query) return null
  if (search.pending) {
    return <LoadingIndicator className="min-h-64 justify-center">{t("searchLoading")}</LoadingIndicator>
  }
  if (search.error) return <MobilePageState title={t("searchError")} onRetry={() => void search.retry()} />
  if (search.conversations.length + search.messages.length + search.people.length === 0) {
    return <MobilePageState title={t("searchNoResults", { query })} />
  }
  return (
    <>
      <MobileSearchGroup
        title={t("searchGroupConversations")}
        count={search.conversations.length}
        onViewAll={onViewAllConversations}
      >
        <MobileConversationRows conversations={search.conversations} highlight={query} onOpen={onOpenConversation} />
      </MobileSearchGroup>
      <MobileSearchGroup title={t("searchGroupMessages")} count={search.messages.length}>
        {search.messages.map((message) => (
          <MobileSearchRow
            key={message.id}
            avatar={<ConversationAvatar conversation={message.conversation} className="size-10" />}
            title={conversationName(message.conversation)}
            detail={<InboxSearchExcerpt message={message} />}
            time={formatTime(message.originatedAt)}
            onOpen={() => onOpenConversation(message.conversation, message.id)}
          />
        ))}
      </MobileSearchGroup>
      <MobileSearchGroup title={t("searchGroupPeople")} count={search.people.length}>
        {search.people.map((person) => {
          const contact = person.kind === InboxSearchPersonKind.InboxSearchPersonContact
          const agent = isAIIdentityType(person.identityType)
          const assistant = person.identityType === OrganizationIdentityType.OrganizationIdentityTypeAssistant
          return (
            <MobileSearchRow
              key={`${person.kind}-${person.id}`}
              avatar={
                <ProfileAvatar
                  imageURL={person.avatarUrl}
                  name={person.displayName}
                  fallback={agent ? "agent" : "person"}
                  seed={person.contactNumber}
                  className="size-10"
                />
              }
              title={highlightName(contactName(person.displayName, person.contactNumber), query)}
              detail={
                contact
                  ? t(person.conversationId ? "searchPersonContact" : "searchPersonNoConversation")
                  : t(assistant ? "contextIdentityAssistant" : agent ? "contextIdentityAgent" : "contextIdentityMember")
              }
              disabled={contact && !person.conversationId}
              onOpen={() => onOpenPerson(person)}
            />
          )
        })}
      </MobileSearchGroup>
    </>
  )
}

/** 按地址参数恢复检索词、限定会话和查看全部状态，从结果详情返回时保留检索上下文。 */
export function MobileInboxSearchPage() {
  const { t } = useTranslation(["inbox", "mobile", "common"])
  const navigate = useNavigate()
  const location = useLocation()
  const [params, setParams] = useSearchParams()
  const { listURL, inboxWindows } = useMobileNavigation()
  const { identity } = useMobileWorkspace()
  const back = useMobileBack(listURL)
  const { ids: recentConversationIds } = useRecentConversations(identity.user.identityId)
  const inputRef = useRef<HTMLInputElement>(null)
  const [text, setText] = useState(() => params.get("q") ?? "")
  const conversationId = params.get("conversation") ?? ""
  // 只有「查看全部」会进入会话分页，其余情况展示分组结果。
  const type = searchTypes.find((value) => value === params.get("type")) ?? "all"
  const search = useInboxSearchResults({
    active: true,
    text,
    // 与桌面端一致：从会话进入时只检索该会话，其余情况检索全部可读消息。
    range: conversationId
      ? InboxSearchRange.InboxSearchRangeConversation
      : InboxSearchRange.InboxSearchRangeReadable,
    conversationId,
    type,
    query: readableInboxQuery,
    recentConversationIds,
  })
  const query = text.trim()
  const scopedConversation = useConversationSummary(conversationId)
  const conversationName = useConversationName()
  const scopedName = scopedConversation.data ? conversationName(scopedConversation.data) : ""

  /** 以替换方式写回检索条件，保留返回来源。 */
  function updateParams(changes: Record<string, string>) {
    const next = new URLSearchParams(params)
    for (const [name, value] of Object.entries(changes)) {
      if (value) next.set(name, value)
      else next.delete(name)
    }
    setParams(next, { replace: true, state: location.state })
  }

  /** 打开会话详情，消息结果同时定位原消息。 */
  function openConversation(conversation: InboxConversationData, messageId = "") {
    void navigate(mobileConversationPath(conversation), {
      state: {
        conversation,
        mobileBack: true,
        ...(messageId ? { locateMessage: { messageId, nonce: Date.now() } } : {}),
      },
    })
  }

  /** 打开人员结果：外部联系人进入客户会话，真人成员进入单聊，AI 员工与本人助理开始新对话。 */
  function openPerson(person: InboxSearchPersonData) {
    if (person.kind === InboxSearchPersonKind.InboxSearchPersonContact) {
      if (person.conversationId)
        void navigate(`/inbox/customer/${person.conversationId}`, { state: { mobileBack: true } })
    } else if (person.agentId) {
      void navigate(`/chats/agent/${crypto.randomUUID()}`, {
        state: {
          draftAgentID: person.agentId,
          draftAssistant: person.identityType === OrganizationIdentityType.OrganizationIdentityTypeAssistant,
          mobileBack: true,
        },
      })
    } else if (person.userId) {
      void navigate(`/contacts/employees/${person.userId}/chat`, { state: { mobileBack: true } })
    }
  }

  return (
    <section
      className="flex h-full min-h-0 flex-col bg-background"
      onTouchMove={() => inputRef.current?.blur()}
    >
      <div className="flex h-12 shrink-0 items-center gap-1 border-b bg-sidebar pr-2 pl-4">
        <div className="relative min-w-0 flex-1">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            ref={inputRef}
            type="search"
            enterKeyHint="search"
            autoFocus={!text}
            value={text}
            aria-label={t(conversationId ? "inbox:searchCurrentConversation" : "common:actions.search")}
            className="h-9 pr-9 pl-9 md:text-base [&::-webkit-search-cancel-button]:hidden"
            onChange={(event) => {
              setText(event.target.value)
              // 修改检索词时离开会话分页，回到分组结果。
              updateParams({ q: event.target.value.trim() ? event.target.value : "", ...(type === "conversations" ? { type: "" } : {}) })
            }}
            onKeyDown={(event) => {
              // 键盘搜索键只收起键盘，结果随输入实时更新。
              if (event.key === "Enter" && !event.nativeEvent.isComposing) event.currentTarget.blur()
            }}
          />
          {text ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="absolute top-1/2 right-0.5 -translate-y-1/2 text-muted-foreground"
              aria-label={t("mobile:clearSearch")}
              onClick={() => {
                setText("")
                updateParams({ q: "", ...(type === "conversations" ? { type: "" } : {}) })
                inputRef.current?.focus()
              }}
            >
              <XIcon />
            </Button>
          ) : null}
        </div>
        <Button type="button" variant="ghost" className="min-h-11 shrink-0 px-3" onClick={back}>
          {t("common:actions.cancel")}
        </Button>
      </div>
      {conversationId ? (
        <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted pr-1 pl-4 text-sm text-muted-foreground">
          <span className="min-w-0 flex-1 truncate">
            {scopedName
              ? t("searchConversationScope", { name: scopedName })
              : t("searchCurrentConversation")}
          </span>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="size-9 text-muted-foreground"
            aria-label={t("searchConversationScopeClear")}
            onClick={() => updateParams({ conversation: "" })}
          >
            <XIcon />
          </Button>
        </div>
      ) : null}
      {search.paged && !search.pending ? (
        <InboxSearchConversationList identity={identity} query={search.nameQuery} history={inboxWindows} mobile>
          {(conversations) => (
            <ul>
              <MobileConversationRows conversations={conversations} highlight={query} onOpen={openConversation} />
            </ul>
          )}
        </InboxSearchConversationList>
      ) : (
        <MobileScrollArea storageKey={`inbox-search:${params.toString()}`} ready={!search.pending}>
          <MobileSearchResults
            search={search}
            query={query}
            onOpenConversation={openConversation}
            onOpenPerson={openPerson}
            onViewAllConversations={() => updateParams({ type: "conversations" })}
          />
        </MobileScrollArea>
      )}
    </section>
  )
}
