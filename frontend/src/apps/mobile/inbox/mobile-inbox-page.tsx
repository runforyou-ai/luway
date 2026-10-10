/** 移动端收件箱：待处理与全部两页并排、跟手滑动切换，页签指示线随滑动进度移动。 */
import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { SearchIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { InboxScope } from "@/api"
import {
  MobileConversationList,
  MobileListHeader,
  useMobileListOptions,
} from "@/apps/mobile/chats/mobile-conversation-list"
import {
  MobileInboxFilter,
  MobileInboxScopes,
  useMobileInboxQuery,
  type MobileInboxQuery,
} from "@/apps/mobile/inbox/mobile-inbox-navigation"
import { mobileSearchPath, useMobileNavigation } from "@/apps/mobile/shared/mobile-navigation"
import { useMobileWorkspace } from "@/apps/mobile/shared/mobile-workspace-layout"
import { Button } from "@/components/ui/button"
import { useInboxAttention } from "@/features/inbox/list/inbox-attention"
import { inboxTabs, normalizeInboxQuery } from "@/features/inbox/list/inbox-query"
import {
  useInboxList,
  usePartitionedInboxList,
  type InboxList,
  type InboxListViewport,
  type PartitionedInboxList,
} from "@/features/inbox/list/use-inbox-list"
import { useInboxListViewport } from "@/features/inbox/list/use-inbox-list-viewport"
import { cn } from "@/lib/utils"

type MobileInboxPaneProps = {
  query: MobileInboxQuery
  covered: boolean
  sorting: boolean
  onSortingChange: (sorting: boolean) => void
}

/** 渲染收件箱标题、页签、当前页筛选和两页横向吸附滚动的列表，滚动位置与地址中的页签保持同步。 */
export function MobileInboxPage() {
  const { t } = useTranslation(["mobile", "common"])
  const navigate = useNavigate()
  const { identity } = useMobileWorkspace()
  const attention = useInboxAttention(identity)
  const { query, changeQuery } = useMobileInboxQuery()
  const [filterOpen, setFilterOpen] = useState(false)
  const [sorting, setSorting] = useState(false)
  // 离开页签时记下该页筛选，回到该页时恢复；记录在登录会话内保留，离开收件箱后仍然有效。
  const { inboxScopeQueries } = useMobileNavigation()
  const pager = useRef<HTMLDivElement>(null)
  const indicator = useRef<HTMLSpanElement>(null)
  const index = inboxTabs.findIndex((tab) => tab.value === query.scope)
  const currentIndex = useRef(index)
  // 当前页使用地址中的筛选，另一页使用离开时记下的筛选或当前可共用的筛选条件。
  const paneQuery = (scope: InboxScope) =>
    scope === query.scope ? query : (inboxScopeQueries.get(scope) ?? normalizeInboxQuery({ ...query, scope }))

  /** 按横向滚动进度移动页签指示线。 */
  function moveIndicator(progress: number) {
    if (indicator.current) indicator.current.style.transform = `translateX(${progress * 100}%)`
  }

  /** 切换当前页签，记下离开页的筛选并恢复目标页的筛选，同时结束置顶排序。 */
  function selectScope(scope: InboxScope) {
    if (scope === query.scope) return
    setSorting(false)
    inboxScopeQueries.set(query.scope, query)
    changeQuery(inboxScopeQueries.get(scope) ?? { scope })
  }

  useLayoutEffect(() => {
    currentIndex.current = index
  }, [index])
  useLayoutEffect(() => {
    // 进入页面和尺寸变化时直接对齐到当前页签，滑动过程中不干预滚动位置。
    const element = pager.current
    if (!element) return
    /** 把滚动位置和指示线对齐到当前页签。 */
    const align = () => {
      element.scrollLeft = currentIndex.current * element.clientWidth
      moveIndicator(currentIndex.current)
    }
    align()
    const observer = new ResizeObserver(align)
    observer.observe(element)
    return () => observer.disconnect()
  }, [])

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobileListHeader
        title={t("inbox.title")}
        sorting={sorting}
        onSortingDone={() => setSorting(false)}
        actions={
          <Button
            variant="ghost"
            size="icon-lg"
            className="shrink-0"
            aria-label={t("common:actions.search")}
            onClick={() => navigate(mobileSearchPath(), { state: { mobileBack: true } })}
          >
            <SearchIcon />
          </Button>
        }
      />
      <MobileInboxScopes
        scope={query.scope}
        pendingCount={attention.data?.pending ?? 0}
        indicatorRef={indicator}
        onSelect={(scope) => {
          // 点击页签平滑滚动到目标页，页签随滚动越过中线时切换。
          const element = pager.current
          const target = inboxTabs.findIndex((tab) => tab.value === scope)
          if (element) element.scrollTo({ left: target * element.clientWidth, behavior: "smooth" })
          else selectScope(scope)
        }}
      />
      <MobileInboxFilter
        key={query.scope}
        query={query}
        onChange={changeQuery}
        onOpenChange={setFilterOpen}
      />
      <div
        ref={pager}
        className={cn(
          "flex min-h-0 flex-1 snap-x snap-mandatory overflow-y-hidden overscroll-x-contain [scrollbar-width:none] [&::-webkit-scrollbar]:hidden",
          sorting ? "overflow-x-hidden" : "overflow-x-auto",
        )}
        onScroll={(event) => {
          const element = event.currentTarget
          if (!element.clientWidth) return
          const progress = element.scrollLeft / element.clientWidth
          moveIndicator(progress)
          const next = inboxTabs[Math.round(progress)]
          if (next) selectScope(next.value)
        }}
      >
        {inboxTabs.map(({ value }) => (
          <div key={value} className="flex min-h-0 w-full shrink-0 snap-start snap-always flex-col">
            {value === InboxScope.Pending ? (
              <MobilePendingPane
                key={JSON.stringify(paneQuery(value))}
                query={paneQuery(value)}
                covered={filterOpen && query.scope === value}
                sorting={false}
                onSortingChange={setSorting}
              />
            ) : (
              <MobileAllPane
                key={JSON.stringify(paneQuery(value))}
                query={paneQuery(value)}
                covered={filterOpen && query.scope === value}
                sorting={sorting}
                onSortingChange={setSorting}
              />
            )}
          </div>
        ))}
      </div>
    </section>
  )
}

/** 待处理页按等待起点读取一条列表。 */
function MobilePendingPane(props: MobileInboxPaneProps) {
  const viewport = useInboxListViewport()
  const list = useInboxList(props.query, viewport, useMobileListOptions())
  return <MobileInboxPaneList {...props} list={list} viewport={viewport} />
}

/** 全部页按置顶区在前、最近活动在后读取列表。 */
function MobileAllPane(props: MobileInboxPaneProps) {
  const viewport = useInboxListViewport()
  const list = usePartitionedInboxList(props.query, viewport, useMobileListOptions())
  return <MobileInboxPaneList {...props} list={list} viewport={viewport} />
}

/** 渲染单页服务会话列表，筛选面板打开期间保持该页列表原位。 */
function MobileInboxPaneList({
  query,
  covered,
  sorting,
  onSortingChange,
  list,
  viewport,
}: MobileInboxPaneProps & {
  list: InboxList | PartitionedInboxList
  viewport: InboxListViewport
}) {
  const { t } = useTranslation("inbox")
  const pending = query.scope === InboxScope.Pending
  useEffect(() => {
    viewport.setCovered(covered)
  }, [covered, viewport])
  return (
    <MobileConversationList
      list={list}
      viewport={viewport}
      showAssignee={!pending}
      showAudience
      emptyTitle={t(pending ? "pendingEmptyTitle" : "emptyTitle")}
      emptyDescription={t(pending ? "pendingEmptyDescription" : "emptyDescription")}
      sorting={sorting}
      onSortingChange={onSortingChange}
    />
  )
}
