/** 全局搜索模态的搜索结果：分组结果、会话分页列表和最近打开。 */
import { useEffect, useRef, type MouseEvent, type ReactNode } from "react"
import { useTranslation } from "react-i18next"

import {
  InboxSearchPersonKind,
  type Identity,
  type InboxConversationData,
} from "@/api"
import { LoadingIndicator } from "@/components/loading-indicator"
import { ProfileAvatar } from "@/components/profile-avatar"
import { Button } from "@/components/ui/button"
import { ConversationAvatar } from "@/features/inbox/conversation-avatar"
import { InboxSearchConversationList } from "@/features/inbox/inbox-search-conversation-list"
import { useContactName } from "@/hooks/use-contact-name"
import { useConversationName } from "@/hooks/use-conversation-name"
import { useConversationTime } from "@/features/inbox/use-conversation-time"
import type { InboxSearchMessageData, InboxSearchState } from "@/features/inbox/use-inbox-search"
import { cn } from "@/lib/utils"
import { isAIIdentityType } from "@/lib/identity-type"

const searchGroupLimit = 6
const markClassName = "bg-transparent font-semibold text-primary"

/** 点击结果或切换项时保持搜索框焦点。 */
function keepSearchFocus(event: MouseEvent) {
  event.preventDefault()
}

/** 不区分大小写地标出名称中的首个匹配片段。 */
export function highlightName(name: string, query: string): ReactNode {
  const index = query ? name.toLowerCase().indexOf(query.toLowerCase()) : -1
  if (index < 0) return name
  return (
    <>
      {name.slice(0, index)}
      <mark className={markClassName}>{name.slice(index, index + query.length)}</mark>
      {name.slice(index + query.length)}
    </>
  )
}

/** 渲染消息结果的发送者与高亮摘要。 */
export function InboxSearchExcerpt({ message }: { message: InboxSearchMessageData }) {
  const { t } = useTranslation("inbox")
  const contactName = useContactName()
  const senderName = contactName(message.senderName, message.senderContactNumber)
  return (
    <>
      {senderName ? t("searchMessageSender", { name: senderName }) : null}
      {message.excerpt.map((segment, segmentIndex) =>
        segment.match ? (
          <mark key={segmentIndex} className={markClassName}>{segment.text}</mark>
        ) : (
          <span key={segmentIndex}>{segment.text}</span>
        ),
      )}
    </>
  )
}

/** 搜索结果分组；传入查看全部文案且结果达到上限时显示查看全部入口，未传入 onViewAll 时入口停用。 */
function SearchGroup({
  title,
  count,
  viewAllLabel,
  onViewAll,
  children,
}: {
  title: string
  count: number
  viewAllLabel?: string
  onViewAll?: () => void
  children: ReactNode
}) {
  if (count === 0) return null
  return (
    <section>
      <h3 className="px-3 pt-2.5 pb-1 text-xs font-semibold text-muted-foreground">{title}</h3>
      {children}
      {viewAllLabel && count >= searchGroupLimit ? (
        <Button
          type="button"
          variant="ghost"
          size="xs"
          disabled={!onViewAll}
          className="ml-1 text-primary"
          onMouseDown={keepSearchFocus}
          onClick={onViewAll}
        >
          {viewAllLabel}
        </Button>
      ) : null}
    </section>
  )
}

/** 搜索结果行，选中态跟随键盘和鼠标；会话行携带 inboxId 供分页列表滚动补偿定位。 */
function SearchResultRow({
  index,
  inboxId,
  selected,
  disabled = false,
  avatar,
  title,
  detail,
  time,
  onSelect,
  onOpen,
}: {
  index: number
  inboxId?: string
  selected: boolean
  disabled?: boolean
  avatar: ReactNode
  title: ReactNode
  detail?: ReactNode
  time?: string
  onSelect: (index: number) => void
  onOpen: () => void
}) {
  return (
    <button
      type="button"
      role="option"
      aria-selected={selected}
      aria-disabled={disabled}
      data-search-index={index}
      data-inbox-id={inboxId}
      className={cn(
        "flex w-full min-w-0 items-center gap-2.5 rounded-md px-3 py-1.5 text-left",
        selected ? "bg-accent text-accent-foreground" : "hover:bg-muted",
        disabled && "cursor-not-allowed opacity-50",
      )}
      onMouseDown={keepSearchFocus}
      onMouseMove={() => onSelect(index)}
      onClick={() => {
        if (!disabled) onOpen()
      }}
    >
      <span className="shrink-0">{avatar}</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm">{title}</span>
        {detail ? (
          <span className={cn("block truncate text-xs text-muted-foreground", selected && "text-accent-foreground/75")}>{detail}</span>
        ) : null}
      </span>
      {time ? <span className="shrink-0 text-xs text-muted-foreground">{time}</span> : null}
    </button>
  )
}

/** 渲染分组结果或会话分页列表。 */
export function InboxSearchPanel({ search, identity }: { search: InboxSearchState; identity: Identity }) {
  const { t } = useTranslation(["inbox", "common"])
  const contactName = useContactName()
  const conversationName = useConversationName()
  const formatTime = useConversationTime()
  const listRef = useRef<HTMLDivElement>(null)
  const query = search.text.trim()

  useEffect(() => {
    // 键盘移动选择时保持选中项可见。
    listRef.current?.querySelector(`[data-search-index="${search.selectedIndex}"]`)?.scrollIntoView({ block: "nearest" })
  }, [search.selectedIndex])

  const conversationRows = (conversations: InboxConversationData[]) =>
    conversations.map((conversation, index) => (
      <SearchResultRow
        key={conversation.id}
        index={index}
        inboxId={conversation.id}
        selected={search.selectedIndex === index}
        avatar={<ConversationAvatar conversation={conversation} className="size-7" />}
        title={highlightName(
          conversationName(conversation),
          search.showRecent ? "" : query,
        )}
        onSelect={search.setActiveIndex}
        onOpen={() => search.open({ kind: "conversation", conversation })}
      />
    ))
  const messageOffset = search.conversations.length
  const peopleOffset = messageOffset + search.messages.length
  const empty = search.conversations.length + search.messages.length + search.people.length === 0

  let body: ReactNode = null
  if (search.showRecent) {
    body = (
      <SearchGroup title={t("searchRecent")} count={search.recentConversations.length}>
        {conversationRows(search.recentConversations)}
      </SearchGroup>
    )
  } else if (query && search.paged && !search.pending) {
    body = (
      <InboxSearchConversationList identity={identity} query={search.nameQuery} onConversationsChange={search.setPagedConversations}>
        {conversationRows}
      </InboxSearchConversationList>
    )
  } else if (query && search.pending) {
    body = <LoadingIndicator className="justify-center py-10">{t("searchLoading")}</LoadingIndicator>
  } else if (query && search.error) {
    body = (
      <div className="flex flex-col items-center gap-3 px-6 py-10 text-sm text-muted-foreground">
        <p>{t("searchError")}</p>
        <Button type="button" variant="outline" size="sm" onClick={() => void search.retry()}>
          {t("common:actions.retry")}
        </Button>
      </div>
    )
  } else if (query && empty) {
    body = <p className="px-6 py-10 text-center text-sm text-muted-foreground">{t("searchNoResults", { query })}</p>
  } else if (query) {
    body = (
      <>
        <SearchGroup
          title={t("searchGroupConversations")}
          count={search.conversations.length}
          viewAllLabel={t("searchViewAll")}
          onViewAll={() => search.setType("conversations")}
        >
          {conversationRows(search.conversations)}
        </SearchGroup>
        <SearchGroup title={t("searchGroupMessages")} count={search.messages.length} viewAllLabel={t("searchViewAll")}>
          {search.messages.map((message, position) => (
            <SearchResultRow
              key={message.id}
              index={messageOffset + position}
              selected={search.selectedIndex === messageOffset + position}
              avatar={<ConversationAvatar conversation={message.conversation} className="size-7" />}
              title={conversationName(message.conversation)}
              detail={<InboxSearchExcerpt message={message} />}
              time={formatTime(message.originatedAt)}
              onSelect={search.setActiveIndex}
              onOpen={() => search.open({ kind: "message", message })}
            />
          ))}
        </SearchGroup>
        <SearchGroup title={t("searchGroupPeople")} count={search.people.length} viewAllLabel={t("searchViewAll")}>
          {search.people.map((person, position) => {
            const contact = person.kind === InboxSearchPersonKind.InboxSearchPersonContact
            const agent = isAIIdentityType(person.identityType)
            return (
              <SearchResultRow
                key={`${person.kind}-${person.id}`}
                index={peopleOffset + position}
                selected={search.selectedIndex === peopleOffset + position}
                disabled={contact && !person.conversationId}
                avatar={<ProfileAvatar imageURL={person.avatarUrl} name={person.displayName} fallback={agent ? "agent" : "person"} seed={person.contactNumber} className="size-7" />}
                title={highlightName(contactName(person.displayName, person.contactNumber), query)}
                detail={
                  contact
                    ? t(person.conversationId ? "searchPersonContact" : "searchPersonNoConversation")
                    : t(person.personal ? "contextIdentityPersonalAgent" : agent ? "contextIdentityAgent" : "contextIdentityMember")
                }
                onSelect={search.setActiveIndex}
                onOpen={() => search.open({ kind: "person", person })}
              />
            )
          })}
        </SearchGroup>
      </>
    )
  }

  return (
    <div
      ref={listRef}
      role="listbox"
      aria-label={t("common:actions.search")}
      data-slot="inbox-search"
      className={cn("min-h-0 flex-1 px-2 py-2", search.paged ? "flex flex-col" : "overflow-y-auto")}
    >
      {body}
    </div>
  )
}
