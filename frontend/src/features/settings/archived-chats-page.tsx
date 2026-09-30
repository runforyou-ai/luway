/** 个人设置中的已归档聊天列表：按类型筛选、按名称搜索，打开或取消归档。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import {
  ConversationType,
  listArchivedConversations,
  type InboxConversationData,
} from "@/api"
import {
  ListToolbar,
  ListToolbarFilter,
  ListToolbarSearch,
  ListToolbarTotal,
} from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { ConversationAvatar } from "@/features/inbox/conversation-avatar"
import { conversationPreview } from "@/features/inbox/conversation-preview"
import { useConversationArchive } from "@/features/inbox/use-conversation-archive"
import { useConversationName } from "@/hooks/use-conversation-name"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { usePagedResource } from "@/hooks/use-resource"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 已归档聊天可筛选的会话类型与对应文案。 */
const chatKinds = [
  { value: ConversationType.ConversationTypeGroup, label: "filterKindGroup" },
  { value: ConversationType.ConversationTypeDirect, label: "filterKindDirect" },
  { value: ConversationType.ConversationTypeAgent, label: "filterKindAgent" },
] as const

/** 按最近活动倒序展示本人已归档的聊天，整行打开聊天，行操作取消归档。 */
export function ArchivedChatsPage() {
  const { t } = useTranslation(["settings", "inbox"])
  const { t: tInbox } = useTranslation("inbox")
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const conversationName = useConversationName()
  const archive = useConversationArchive()
  const { searchParams, setParameters, query, search, setSearch } = useListSearchParams()
  const kind = optionalWailsEnum(ConversationType, searchParams.get("kind")) ?? ConversationType.$zero
  const list = usePagedResource(
    resourceKeys.archivedConversations({ query, kind, pageSize: 50 }),
    (page, signal) => listArchivedConversations({ search: query, kind, page, pageSize: 50 }, signal),
    { select: (data) => ({ items: data.conversations, page: data.page }), itemKey: (conversation) => conversation.id },
  )
  const conversations = list.data?.items ?? []

  return (
    <section className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("archivedChats.title")} description={t("archivedChats.description")} />

      <ListToolbar>
        <ListToolbarSearch
          value={search}
          aria-label={t("archivedChats.search")}
          onChange={(event) => setSearch(event.target.value)}
        />
        <ListToolbarFilter
          label={t("inbox:filterKind")}
          allLabel={t("inbox:filterAllKinds")}
          value={kind}
          options={chatKinds.map((option) => ({ value: option.value, label: tInbox(option.label) }))}
          onValueChange={(value) => setParameters({ kind: value || null })}
        />
        <ListToolbarTotal count={list.data?.total} />
      </ListToolbar>

      <ResourceListLayout resources={list} errorMessage={t("archivedChats.loadError")} more={list.more}>
        <ResourceTable<InboxConversationData>
          columns={[
            {
              key: "name",
              header: t("archivedChats.columns.name"),
              cellClassName: "min-w-0",
              cell: (conversation) => (
                <ResourceRowIdentity
                  leading={<ConversationAvatar conversation={conversation} className="size-9 shrink-0" />}
                  name={conversationName(conversation)}
                  secondary={tInbox(chatKinds.find((option) => option.value === conversation.type)?.label ?? "filterKindDirect")}
                  description={conversationPreview(conversation, tInbox)}
                />
              ),
            },
            {
              key: "time",
              header: t("archivedChats.archivedAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (conversation) =>
                conversation.archivedAt
                  ? t("archivedChats.archivedAt", { time: formatDateTime(conversation.archivedAt) })
                  : null,
            },
          ]}
          rows={conversations}
          rowKey={(conversation) => conversation.id}
          empty={t("archivedChats.empty")}
          onRowActivate={(conversation) => navigate(`/chats?conversation=${conversation.id}`)}
          rowActions={(conversation) => [
            {
              key: "unarchive",
              label: t("inbox:conversationUnarchive"),
              disabled: archive.isSaving(conversation.id),
              onSelect: () => void archive.save(conversation.id, false),
            },
          ]}
        />
      </ResourceListLayout>
    </section>
  )
}
