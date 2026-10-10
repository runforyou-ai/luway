/** 知识库文档、问答列表共用的读取、页头、搜索和召回测试外壳。 */
import { Fragment, useRef, useState, type ReactNode } from "react"
import type { QueryKey } from "@tanstack/react-query"
import { SearchCheckIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useLocation, useParams } from "react-router"

import { getKnowledgeBase, type KnowledgeBaseCategory, type PageInfo } from "@/api"
import {
  ListToolbar,
  ListToolbarReset,
  ListToolbarSearch,
  ListToolbarTotal,
} from "@/components/list-toolbar"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useListScrollRestore } from "@/hooks/use-list-scroll-restore"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { usePagedResource, useResource } from "@/hooks/use-resource"
import { KnowledgeRetrievalSheet } from "./knowledge-retrieval-sheet"

const pageSize = 20

/** 知识库内容列表的读取参数，页码由滚动加载逐页给出。 */
type KnowledgeContentListParameters = {
  keyword: string
  pageSize: number
}

/** 按知识库切换列表实例，隔离弹窗、上传批次和滚动恢复状态。 */
export function KnowledgeContentRoute({
  children,
}: {
  children: (ids: { knowledgeBaseId: string }) => ReactNode
}) {
  const { knowledgeBaseId = "" } = useParams()
  return (
    <Fragment key={knowledgeBaseId}>
      {children({ knowledgeBaseId })}
    </Fragment>
  )
}

/** 滚动加载知识库内容，列表中有处理中的条目时每 2 秒刷新已加载的页。 */
export function useKnowledgeContentList<T extends { page: PageInfo }, I extends { id: string }>({
  knowledgeBaseId,
  section,
  listKey,
  load,
  items,
  processing,
}: {
  knowledgeBaseId: string
  section: "documents" | "qa"
  listKey: (parameters: KnowledgeContentListParameters) => QueryKey
  load: (parameters: KnowledgeContentListParameters & { page: number }, signal: AbortSignal) => Promise<T>
  items: (data: T) => readonly I[]
  processing: (item: I) => boolean
}) {
  const location = useLocation()
  const listSearch = useListSearchParams()
  const parameters = { keyword: listSearch.query, pageSize }
  const base = useResource(resourceKeys.knowledgeBase(knowledgeBaseId), (signal) =>
    getKnowledgeBase(knowledgeBaseId, signal),
  )
  const list = usePagedResource(
    listKey(parameters),
    (page, signal) => load({ ...parameters, page }, signal),
    {
      select: (data) => ({ items: items(data), page: data.page }),
      itemKey: (item) => item.id,
      staleTime: 0,
      keepPreviousData: true,
      refetchInterval: (loaded) => (loaded.some(processing) ? 2000 : false),
    },
  )
  const listPath = `/knowledge-bases/${knowledgeBaseId}/${section}`
  const scroll = useListScrollRestore(
    `${listPath}${location.search}`,
    Boolean(list.data && !list.more.restoring && base.data),
  )

  return {
    ...listSearch,
    base,
    list,
    listPath,
    scroll,
    title: base.data?.name,
  }
}

/** 渲染知识库内容列表的页头、搜索工具栏、读取状态和召回测试面板。 */
export function KnowledgeContentListShell({
  list,
  knowledgeBaseId,
  category,
  fallbackTitle,
  description,
  searchLabel,
  errorMessage,
  actions,
  children,
}: {
  list: ReturnType<typeof useKnowledgeContentList>
  knowledgeBaseId: string
  category: KnowledgeBaseCategory
  fallbackTitle: string
  description: string
  searchLabel: string
  errorMessage: string
  actions: ReactNode
  children: ReactNode
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const [retrievalOpen, setRetrievalOpen] = useState(false)
  const retrievalTrigger = useRef<HTMLButtonElement>(null)
  const { search, setSearch, setParameters } = list

  return (
    <>
      <PageHeader title={list.title || fallbackTitle} description={description} backTo="/knowledge-bases">
        {list.list.data ? (
          <>
            <Button
              ref={retrievalTrigger}
              variant="ghost"
              size="icon-sm"
              className="shrink-0"
              aria-label={t("retrieval.action")}
              title={t("retrieval.action")}
              onClick={() => setRetrievalOpen(true)}
            >
              <SearchCheckIcon />
            </Button>
            {actions}
          </>
        ) : null}
      </PageHeader>
      <ListToolbar>
        <ListToolbarSearch
          value={search}
          aria-label={searchLabel}
          onChange={(event) => setSearch(event.target.value)}
        />
        {search ? (
          <ListToolbarReset
            onClick={() => {
              setSearch("")
              setParameters({ q: null })
            }}
          >
            {t("common:actions.clearFilters")}
          </ListToolbarReset>
        ) : null}
        <ListToolbarTotal count={list.list.data?.total} />
      </ListToolbar>
      <PageContent ref={list.scroll.ref} onScroll={list.scroll.onScroll}>
        <ResourceContent resources={[list.base, list.list]} errorMessage={errorMessage}>
          {children}
        </ResourceContent>
      </PageContent>
      <KnowledgeRetrievalSheet
        open={retrievalOpen}
        onOpenChange={setRetrievalOpen}
        knowledgeBaseId={knowledgeBaseId}
        category={category}
        triggerRef={retrievalTrigger}
      />
    </>
  )
}
