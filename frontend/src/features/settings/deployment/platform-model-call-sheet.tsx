/** 平台模型调用详情侧栏、调用状态徽标与进行中调用的刷新间隔。 */
import { useTranslation } from "react-i18next"

import { AIModelCallStatus, getPlatformAIModelCall, type AIModelCallStatusId } from "@/api"
import { ResourceContent } from "@/components/resource-content"
import { StatusBadge } from "@/components/status-badge"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useResource } from "@/hooks/use-resource"

/** 有进行中的调用时刷新调用列表与详情的间隔毫秒数。 */
export const callPollingInterval = 3000

/** 各调用状态的徽标样式。 */
const statusVariants: Record<AIModelCallStatusId, "success" | "warning" | "destructive" | "muted"> = {
  [AIModelCallStatus.AIModelCallStatusRunning]: "warning",
  [AIModelCallStatus.AIModelCallStatusSucceeded]: "success",
  [AIModelCallStatus.AIModelCallStatusFailed]: "destructive",
  [AIModelCallStatus.AIModelCallStatusCanceled]: "muted",
  [AIModelCallStatus.AIModelCallStatusTimedOut]: "destructive",
}

/** 按状态显示调用或上游尝试的状态徽标。 */
export function PlatformModelCallStatus({ status }: { status: AIModelCallStatusId }) {
  const { t } = useTranslation("deployment")
  return <StatusBadge variant={statusVariants[status]}>{t(`platformCalls.statuses.${status}`)}</StatusBadge>
}

/** 返回开始到结束的秒数，进行中的调用返回空。 */
function durationSeconds(createdAt: string, finishedAt: string | null) {
  if (!finishedAt) return null
  return ((Date.parse(finishedAt) - Date.parse(createdAt)) / 1000).toFixed(1)
}

/** 按调用编号打开侧栏，callId 为空时关闭；展示调用归属、用量、失败原因与按顺序排列的上游尝试。 */
export function PlatformModelCallSheet({ callId, onClose }: { callId: string; onClose: () => void }) {
  const { t } = useTranslation("deployment")
  const { formatDateTime } = useDateTime()
  // 打开时重新读取，调用进行中时定时刷新直到结束。
  const detail = useResource(resourceKeys.platformAIModelCall(callId), (signal) => getPlatformAIModelCall(callId, signal), {
    enabled: Boolean(callId),
    staleTime: 0,
    refetchInterval: (current) =>
      current?.call.status === AIModelCallStatus.AIModelCallStatusRunning ? callPollingInterval : false,
  })
  const data = detail.data?.call.id === callId ? detail.data : undefined
  const duration = data ? durationSeconds(data.call.createdAt, data.call.finishedAt) : null

  return (
    <Sheet open={Boolean(callId)} onOpenChange={(open) => (open ? undefined : onClose())}>
      <SheetContent className="w-full gap-0 p-0 sm:max-w-xl">
        <SheetHeader className="border-b px-6 py-4 pr-12">
          <SheetTitle>{data?.call.modelName ?? t("platformCalls.sheet.title")}</SheetTitle>
          <SheetDescription>{data ? formatDateTime(data.call.createdAt) : null}</SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1">
          <div className="p-6">
            <ResourceContent resources={detail} errorMessage={t("platformCalls.sheet.loadError")}>
              {data ? (
                <div className="space-y-9">
                  <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm">
                    <dt className="text-muted-foreground">{t("platformCalls.statusFilter")}</dt>
                    <dd>
                      <PlatformModelCallStatus status={data.call.status} />
                    </dd>
                    <dt className="text-muted-foreground">{t("platformCalls.sheet.workspace")}</dt>
                    <dd className="min-w-0 break-words">{data.call.workspaceName}</dd>
                    <dt className="text-muted-foreground">{t("platformCalls.sheet.usage")}</dt>
                    <dd>{t(`platformCalls.usages.${data.call.usage}`)}</dd>
                    <dt className="text-muted-foreground">{t("platformCalls.sheet.actor")}</dt>
                    <dd>{t(`platformCalls.actors.${data.call.actor}`)}</dd>
                    {duration !== null ? (
                      <>
                        <dt className="text-muted-foreground">{t("platformCalls.sheet.duration")}</dt>
                        <dd className="tabular-nums">{t("platformCalls.sheet.durationSeconds", { value: duration })}</dd>
                      </>
                    ) : null}
                    <dt className="text-muted-foreground">{t("platformCalls.sheet.tokens")}</dt>
                    <dd className="tabular-nums">
                      {t("platformCalls.sheet.tokensDetail", {
                        input: data.call.inputTokens.toLocaleString(),
                        cached: data.call.cachedInputTokens.toLocaleString(),
                        output: data.call.outputTokens.toLocaleString(),
                      })}
                    </dd>
                    {data.call.errorMessage ? (
                      <>
                        <dt className="text-muted-foreground">{t("platformCalls.sheet.error")}</dt>
                        <dd className="min-w-0 break-words whitespace-pre-wrap">{data.call.errorMessage}</dd>
                      </>
                    ) : null}
                  </dl>
                  <section className="space-y-3">
                    <h3 className="text-sm font-medium">{t("platformCalls.sheet.attempts")}</h3>
                    <ol className="divide-y border-y">
                      {data.attempts.map((attempt, index) => (
                        <li key={attempt.id} className="space-y-1 py-3 text-sm">
                          <div className="flex min-w-0 items-center gap-2">
                            <span className="w-5 shrink-0 text-center text-xs text-muted-foreground tabular-nums">{index + 1}</span>
                            <span className="min-w-0 truncate">
                              {attempt.providerName} · <span className="font-mono text-xs">{attempt.identifier}</span>
                            </span>
                            <span className="ml-auto">
                              <PlatformModelCallStatus status={attempt.status} />
                            </span>
                          </div>
                          <p className="pl-7 text-xs text-muted-foreground tabular-nums">
                            {t("platformCalls.tokens", {
                              input: attempt.inputTokens.toLocaleString(),
                              output: attempt.outputTokens.toLocaleString(),
                            })}
                          </p>
                          {attempt.errorMessage ? (
                            <p className="pl-7 text-xs break-words whitespace-pre-wrap text-destructive">{attempt.errorMessage}</p>
                          ) : null}
                        </li>
                      ))}
                    </ol>
                  </section>
                </div>
              ) : null}
            </ResourceContent>
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}
