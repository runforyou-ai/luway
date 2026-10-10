/** 待补知识的处理内容：读取条目与知识库，展示来源对话，当前成员可以处理时把 AI 起草的问答编辑后加入知识库，或忽略该条目。 */
import { useEffect, useRef } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import {
  getKnowledgeGap,
  KnowledgeBaseCategory,
  KnowledgeGapDraftStatus,
  KnowledgeGapSource,
  KnowledgeGapStatus,
  listKnowledgeBases,
  type KnowledgeBase,
  type KnowledgeGap,
} from "@/api"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { resolveAppPlatform } from "@/platform/app-platform"

import { useKnowledgeGapDismiss } from "./use-knowledge-gap-actions"
import { KnowledgeGapForm } from "./knowledge-gap-form"
import { ServiceTranscript } from "@/components/service-transcript"

/** 起草期间轮询详情的间隔。 */
const draftPollInterval = 2000

/** 读取条目与知识库列表；gapId 为空时不读取，起草期间持续刷新条目。 */
export function useKnowledgeGapResources(gapId: string) {
  const invalidate = useResourceInvalidator()
  const gap = useResource(
    resourceKeys.knowledgeGap(gapId),
    (signal) => getKnowledgeGap(gapId, signal),
    {
      enabled: Boolean(gapId),
      staleTime: 0,
      // 待处理条目起草期间持续读取，草稿就绪后停止。
      refetchInterval: (data) =>
        data?.status === KnowledgeGapStatus.Pending &&
        data.draftStatus === KnowledgeGapDraftStatus.Pending
          ? draftPollInterval
          : false,
    },
  )
  const bases = useResource(resourceKeys.knowledgeBases(), () => listKnowledgeBases(), {
    enabled: Boolean(gapId),
    staleTime: 0,
  })
  const data = gap.data?.id === gapId ? gap.data : undefined
  const draftStatus = useRef(data?.draftStatus)
  useEffect(() => {
    // 起草结束后刷新清单上的草稿标记。
    if (
      draftStatus.current === KnowledgeGapDraftStatus.Pending &&
      data?.draftStatus !== KnowledgeGapDraftStatus.Pending
    ) {
      void invalidate(resourceKeys.knowledgeGaps())
    }
    draftStatus.current = data?.draftStatus
  }, [data?.draftStatus, invalidate])

  return { gap, bases, data }
}

/** 返回条目的来源、咨询分类与发生时间摘要。 */
export function useKnowledgeGapSummary(gap: KnowledgeGap | undefined) {
  const { t } = useTranslation("agents")
  const { formatDateTime } = useDateTime()
  if (!gap) return ""
  return [
    t(`performance.gapSources.${gap.source}`),
    gap.categoryName || t("performance.uncategorized"),
    t(`performance.gapTimes.${gap.source}`, { time: formatDateTime(gap.occurredAt) }),
  ].join(" · ")
}

/** 展示读取状态与处理内容，处理完成后由 onHandled 决定下一条。 */
export function KnowledgeGapContent({
  resources,
  onHandled,
}: {
  resources: ReturnType<typeof useKnowledgeGapResources>
  onHandled: () => void
}) {
  const { t } = useTranslation("agents")
  const { gap, bases, data } = resources
  return (
    <ResourceContent
      resources={[gap, bases]}
      errorMessage={t("performance.gapSheet.loadError")}
    >
      {data && bases.data ? (
        <KnowledgeGapDetail
          key={data.id}
          gap={data}
          bases={bases.data.knowledgeBases}
          onHandled={onHandled}
        />
      ) : null}
    </ResourceContent>
  )
}

/** 展示来源对话；已加入知识库的条目只展示结果，没有问答知识库时只能忽略，其余展示问答表单。 */
function KnowledgeGapDetail({
  gap,
  bases,
  onHandled,
}: {
  gap: KnowledgeGap
  bases: KnowledgeBase[]
  onHandled: () => void
}) {
  const { t } = useTranslation("agents")
  const navigate = useNavigate()
  const { dismiss, dismissing } = useKnowledgeGapDismiss(gap, onHandled)
  const qaBases = bases.filter(
    (item) => item.category === KnowledgeBaseCategory.QA,
  )
  const base = bases.find((item) => item.id === gap.knowledgeBaseId)
  const dismissButton =
    gap.status === KnowledgeGapStatus.Pending && gap.handleable ? (
      <Button type="button" variant="outline" disabled={dismissing} onClick={() => void dismiss()}>
        {t("performance.dismissGap")}
      </Button>
    ) : null

  return (
    <div className="space-y-9">
      <KnowledgeGapConversation gap={gap} />
      {gap.status === KnowledgeGapStatus.Accepted ? (
        <div className="flex items-center justify-between gap-3 text-sm">
          <p className="min-w-0 truncate text-muted-foreground">
            {base
              ? `${t("performance.gapSheet.accepted")} · ${base.name}`
              : t("performance.gapSheet.accepted")}
          </p>
          {/* 知识库管理只在 Web 与桌面端提供。 */}
          {base && gap.qaEntryId && resolveAppPlatform() !== "mobile" ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => navigate(`/knowledge-bases/${base.id}/qa/${gap.qaEntryId}/edit`)}
            >
              {t("performance.gapSheet.viewEntry")}
            </Button>
          ) : null}
        </div>
      ) : !gap.handleable ? null : qaBases.length === 0 ? (
        <div className="space-y-9">
          <p className="text-sm text-muted-foreground">{t("performance.gapSheet.noKnowledgeBase")}</p>
          {dismissButton ? <div className="flex justify-end">{dismissButton}</div> : null}
        </div>
      ) : (
        <KnowledgeGapForm
          gap={gap}
          bases={qaBases}
          dismissButton={dismissButton}
          dismissing={dismissing}
          onHandled={onHandled}
        />
      )}
    </div>
  )
}

/** 列出来源周期的对客沟通，客户提问确定后突出显示。 */
function KnowledgeGapConversation({ gap }: { gap: KnowledgeGap }) {
  // 复核来源的提问由起草确定，草稿就绪前不突出显示。
  const questionMessageId =
    gap.source === KnowledgeGapSource.KnowledgeGap ||
    gap.source === KnowledgeGapSource.InsufficientEvidence ||
    gap.draftStatus === KnowledgeGapDraftStatus.Ready
      ? gap.questionMessageId
      : ""

  return (
    <ServiceTranscript
      conversationId={gap.conversationId}
      messages={gap.messages}
      highlightedMessageId={questionMessageId}
    />
  )
}
