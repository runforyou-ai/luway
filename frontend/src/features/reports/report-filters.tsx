/** 客服报表页共用的工具栏筛选。 */
import { useTranslation } from "react-i18next"

import { listInboxChannels, ServiceAudience, ServiceSource } from "@/api"
import { ListToolbarFilter } from "@/components/list-toolbar"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

export { useReportSearchParams } from "@/hooks/use-report-search-params"

/** 按接待渠道筛选，每次挂载重新读取渠道列表。 */
export function ReportChannelFilter({ value, onValueChange, audience, serviceSource }: { value: string; onValueChange: (value: string) => void; audience?: ServiceAudience; serviceSource?: ServiceSource }) {
  const { t } = useTranslation("agents")
  const channels = useResource(resourceKeys.inboxChannels(), () => listInboxChannels(), {
    staleTime: 0,
  })
  if (serviceSource === ServiceSource.Direct) return null
  return (
    <ListToolbarFilter
      label={t("performance.channel")}
      allLabel={t("performance.allChannels")}
      value={value}
      options={(channels.data ?? []).filter((channel) => !audience || channel.audience === audience).map((channel) => ({
        value: channel.id,
        label: channel.name,
      }))}
      onValueChange={onValueChange}
    />
  )
}

/** 按服务对象和来源筛选，变更时由页面清空渠道选择。 */
export function ReportServiceFilters({ audience, serviceSource, onAudienceChange, onSourceChange }: {
  audience?: ServiceAudience
  serviceSource?: ServiceSource
  onAudienceChange: (value: string) => void
  onSourceChange: (value: string) => void
}) {
  const { t } = useTranslation("agents")
  const sources: { value: ServiceSource; label: string }[] = [{ value: ServiceSource.Channel, label: t("performance.channelSource") }]
  if (audience !== ServiceAudience.Customer) {
    sources.push({ value: ServiceSource.Direct, label: t("performance.directSource") })
  }
  return (
    <>
      <ListToolbarFilter
        label={t("performance.audience")}
        value={audience ?? ""}
        options={[
          { value: "", label: t("performance.allServices") },
          { value: ServiceAudience.Customer, label: t("performance.customerService") },
          { value: ServiceAudience.Employee, label: t("performance.employeeService") },
        ]}
        onValueChange={onAudienceChange}
      />
      <ListToolbarFilter
        label={t("performance.serviceSource")}
        allLabel={t("performance.allSources")}
        value={serviceSource ?? ""}
        options={sources}
        onValueChange={onSourceChange}
      />
    </>
  )
}

/** 从地址参数中读取支持的服务对象与来源。 */
export function reportServiceScope(parameters: URLSearchParams) {
  const audience = parameters.get("audience")
  const source = parameters.get("source")
  return {
    audience: audience === ServiceAudience.Customer || audience === ServiceAudience.Employee ? audience : undefined,
    serviceSource: source === ServiceSource.Channel || source === ServiceSource.Direct ? source : undefined,
  }
}
