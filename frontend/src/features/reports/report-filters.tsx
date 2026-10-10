/** 客服报表页共用的地址参数与工具栏筛选。 */
import { useCallback } from "react"
import { useTranslation } from "react-i18next"
import { useSearchParams } from "react-router"

import { listInboxChannels } from "@/api"
import { ListToolbarFilter } from "@/components/list-toolbar"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 读取并更新报表页地址参数：值等于 defaults 中的默认值时从地址中移除，更新时未给出的 openItems 参数随之关闭。 */
export function useReportSearchParams<K extends string>(
  defaults: Record<K, string>,
  openItems: readonly NoInfer<K>[],
) {
  const [searchParams, setSearchParams] = useSearchParams()
  const setParameters = useCallback(
    (changes: Partial<Record<K, string>>) => {
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current)
          for (const name of openItems) if (changes[name] === undefined) next.delete(name)
          for (const [name, value] of Object.entries(changes) as [K, string | undefined][]) {
            if (value === undefined) continue
            if (value === defaults[name]) next.delete(name)
            else next.set(name, value)
          }
          return next
        },
        { replace: true },
      )
    },
    [defaults, openItems, setSearchParams],
  )
  return [searchParams, setParameters] as const
}

/** 按接待渠道筛选，每次挂载重新读取渠道列表。 */
export function ReportChannelFilter({ value, onValueChange }: { value: string; onValueChange: (value: string) => void }) {
  const { t } = useTranslation("agents")
  const channels = useResource(resourceKeys.inboxChannels(), () => listInboxChannels(), {
    staleTime: 0,
  })
  return (
    <ListToolbarFilter
      label={t("performance.channel")}
      allLabel={t("performance.allChannels")}
      value={value}
      options={(channels.data ?? []).map((channel) => ({
        value: channel.id,
        label: channel.name,
      }))}
      onValueChange={onValueChange}
    />
  )
}
