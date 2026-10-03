/** 工作区设置的积分页：查看可用积分、今天的每日赠送与积分流水。 */
import { useEffect, useRef } from "react"
import { useTranslation } from "react-i18next"

import {
  AIModelCallStatus,
  CreditEntryKind,
  getCreditBalance,
  listCreditEntries,
  type CreditEntryData,
} from "@/api"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { CreditBalanceTiles, CreditEntryTable } from "@/features/settings/credit-ledger"
import { resourceKeys } from "@/hooks/resource-keys"
import { usePagedResource, useResource, useResourceInvalidator } from "@/hooks/use-resource"

/** 有进行中的模型调用时刷新余额与流水的间隔毫秒数。 */
export const runningRefreshInterval = 3000

/** 没有进行中的模型调用时刷新余额与流水的间隔毫秒数，用于发现新的调用、调整与过期。 */
export const idleRefreshInterval = 30_000

/** 判断流水是否为进行中的模型调用。 */
export function isRunningCall(entry: CreditEntryData) {
  return entry.kind === CreditEntryKind.CreditEntryKindModelCall && entry.callStatus === AIModelCallStatus.AIModelCallStatusRunning
}

/** 渲染积分余额与按时间倒序的积分流水并定时刷新，有进行中的模型调用时缩短间隔，全部结束后再刷新一次余额。 */
export function CreditsPage() {
  const { t } = useTranslation("settings")
  const invalidate = useResourceInvalidator()
  const entries = usePagedResource(
    resourceKeys.creditEntries({ pageSize: 50 }),
    (page, signal) => listCreditEntries({ page, pageSize: 50 }, signal),
    {
      select: (data) => ({ items: data.entries, page: data.page }),
      itemKey: (entry) => `${entry.kind}:${entry.id}`,
      staleTime: 0,
      refetchInterval: (items) => (items.some(isRunningCall) ? runningRefreshInterval : idleRefreshInterval),
    },
  )
  const running = entries.data?.items.some(isRunningCall) ?? false
  const balance = useResource(resourceKeys.creditBalance(), (signal) => getCreditBalance(signal), {
    staleTime: 0,
    refetchInterval: running ? runningRefreshInterval : idleRefreshInterval,
  })
  // 进行中的调用全部结算后，余额按实际扣除重新读取。
  const wasRunning = useRef(false)
  useEffect(() => {
    if (wasRunning.current && !running) void invalidate(resourceKeys.creditBalance())
    wasRunning.current = running
  }, [running, invalidate])

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("credits.title")} description={t("credits.description")} />
      <ResourceListLayout resources={[balance, entries]} errorMessage={t("credits.loadError")} more={entries.more}>
        <div className="space-y-6">
          {balance.data ? <CreditBalanceTiles balance={balance.data} /> : null}
          <CreditEntryTable entries={entries.data?.items ?? []} />
        </div>
      </ResourceListLayout>
    </div>
  )
}
