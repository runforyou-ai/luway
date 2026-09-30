/** 会话名称搜索的「查看全部」分页列表：复用收件箱列表控制器分页读取，并随同步失效重读。 */
import { useEffect, type ReactNode } from "react"
import { useTranslation } from "react-i18next"

import type { Identity, InboxConversationData, InboxQuery } from "@/api"
import { Button } from "@/components/ui/button"
import type { InboxListBookmark } from "./inbox-list-controller"
import { InboxListPanel } from "./inbox-list-panel"
import { useInboxList } from "./use-inbox-list"
import { useInboxListViewport } from "./use-inbox-list-viewport"

/** 按名称搜索查询分页展示全部命中会话，行元素需带 data-inbox-id 供滚动补偿定位；传入 history 时离开后恢复已加载窗口与滚动锚点。 */
export function InboxSearchConversationList({
  identity,
  query,
  history,
  mobile = false,
  onConversationsChange,
  children,
}: {
  identity: Identity
  query: InboxQuery
  history?: Map<string, InboxListBookmark>
  mobile?: boolean
  onConversationsChange?: (conversations: InboxConversationData[]) => void
  children: (conversations: InboxConversationData[]) => ReactNode
}) {
  const { t } = useTranslation(["inbox", "common"])
  const viewport = useInboxListViewport()
  const list = useInboxList(query, viewport, { identity, active: true, history })
  const { conversations } = list
  const ids = conversations.map((conversation) => conversation.id).join(",")

  useEffect(() => {
    // 行集合变化时同步给键盘选择，内容更新不改变选择位置。
    onConversationsChange?.(conversations)
  }, [ids, onConversationsChange])

  // 读取完成且前后都没有更多命中时才展示无结果。
  const empty = list.revision > 0 && !list.error && !list.ids.length && !list.hasBefore && !list.hasAfter
  return (
    <InboxListPanel list={list} viewport={viewport} mobile={mobile}>
      {mobile && list.revision === 0 && list.error ? (
        // 移动端列表面板不展示首次读取失败，由此处提供重试。
        <div className="flex flex-col items-center gap-3 px-6 py-10 text-sm text-muted-foreground">
          <p>{t("searchError")}</p>
          <Button type="button" variant="outline" size="sm" onClick={() => void list.retry()}>
            {t("common:actions.retry")}
          </Button>
        </div>
      ) : empty ? (
        <p className="px-6 py-10 text-center text-sm text-muted-foreground">{t("searchNoResults", { query: query.search })}</p>
      ) : (
        children(conversations)
      )}
    </InboxListPanel>
  )
}
