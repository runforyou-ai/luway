/** 移动端待补知识：列出本人负责的 AI 员工待处理的条目，逐条补充到知识库或忽略。 */
import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, Navigate, useLocation, useNavigate, useSearchParams } from "react-router"

import { KnowledgeGapStatus, listKnowledgeGaps } from "@/api"
import { useMobileBack } from "@/apps/mobile/mobile-navigation"
import { MobilePageHeader } from "@/apps/mobile/mobile-page"
import { MobilePagedList } from "@/apps/mobile/mobile-paged-list"
import {
  KnowledgeGapContent,
  useKnowledgeGapResources,
  useKnowledgeGapSummary,
} from "@/features/agents/knowledge-gap-detail"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"

const listPath = "/me/knowledge-gaps"
const reviewPath = `${listPath}/review`
const pageSize = 50

/** 返回本人负责的待处理条目的分页查询条件。 */
function pendingQuery(page: number) {
  return {
    channelId: "",
    agentId: "",
    mine: true,
    status: KnowledgeGapStatus.KnowledgeGapStatusPending,
    page,
    pageSize,
  }
}

/** 逐页列出待处理条目，行内展示客户提问、咨询分类、来源、草稿状态与发生时间。 */
export function MobileKnowledgeGapsPage() {
  const { t } = useTranslation(["agents", "mobile"])
  const { formatDateTime } = useDateTime()
  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("agents:performance.tabs.knowledgeGaps")} backTo="/me" />
      <MobilePagedList
        storageKey="knowledge-gaps"
        labels={{
          loadError: t("agents:performance.gapSheet.loadError"),
          empty: t("mobile:knowledgeGaps.empty"),
          allLoaded: t("mobile:knowledgeGaps.allLoaded"),
        }}
        source={(page) => {
          const query = pendingQuery(page)
          return {
            key: resourceKeys.knowledgeGaps(query),
            load: () => listKnowledgeGaps(query),
          }
        }}
        select={(data) => ({ items: data.gaps, page: data.page })}
      >
        {(gaps) => (
          <ul className="divide-y border-b">
            {gaps.map((gap) => (
              <li key={gap.id}>
                <Link
                  to={`${reviewPath}?gap=${gap.id}`}
                  state={{ mobileBack: true }}
                  className="flex min-h-18 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                >
                  <span className="grid min-w-0 flex-1 gap-0.5">
                    <span className="truncate text-[15px] font-medium">
                      {gap.question || t("agents:performance.noQuestion")}
                    </span>
                    <span className="truncate text-xs text-muted-foreground">
                      {[
                        gap.categoryName || t("agents:performance.uncategorized"),
                        t(`agents:performance.gapSources.${gap.source}`),
                        gap.hasDraft ? t("agents:performance.hasDraft") : "",
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                    <span className="truncate text-xs text-muted-foreground">
                      {t(`agents:performance.gapTimes.${gap.source}`, {
                        time: formatDateTime(gap.occurredAt),
                      })}
                    </span>
                  </span>
                  <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </MobilePagedList>
    </section>
  )
}

/** 处理地址中指定的条目，未指定时回到清单；处理完成后进入清单中的下一条，没有下一条时回到清单。 */
export function MobileKnowledgeGapReviewPage() {
  const { t } = useTranslation("agents")
  const navigate = useNavigate()
  const location = useLocation()
  const client = useQueryClient()
  const [searchParams] = useSearchParams()
  const gapId = searchParams.get("gap") ?? ""
  const back = useMobileBack(listPath)
  const resources = useKnowledgeGapResources(gapId)
  const summary = useKnowledgeGapSummary(resources.data)
  // 进入时记下清单已加载各页的条目顺序，切换条目不重建页面，顺序沿用到离开。
  const [ids] = useState(() => {
    const loaded: string[] = []
    for (let page = 1; ; page++) {
      const data = client.getQueryData<Awaited<ReturnType<typeof listKnowledgeGaps>>>(
        resourceKeys.knowledgeGaps(pendingQuery(page)),
      )
      if (!data) return loaded
      loaded.push(...data.gaps.map((gap) => gap.id))
    }
  })

  if (!gapId) {
    return <Navigate to={listPath} replace />
  }

  /** 按进入时的清单顺序进入下一条，同一路径内切换条目。 */
  function handled() {
    const index = ids.indexOf(gapId)
    const next = index >= 0 ? ids[index + 1] : undefined
    if (!next) {
      back()
      return
    }
    void navigate(`${reviewPath}?gap=${next}`, { replace: true, state: location.state })
  }

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("performance.gapSheet.title")} backTo={listPath} />
      <div className="app-form min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
        {summary ? <p className="mb-4 text-sm text-muted-foreground">{summary}</p> : null}
        <KnowledgeGapContent resources={resources} onHandled={handled} />
      </div>
    </section>
  )
}
