/** 平台后台的监控页：业务使用页展示各工作区的服务指标，运行状态与近期错误页展示服务端与后台任务的运行情况。 */
import { useTranslation } from "react-i18next"

import { AdminPage } from "@/features/admin/admin-page"
import type { AdminOverviewSection } from "@/features/admin/admin-paths"
import { PlatformErrorListTab } from "@/features/platform/platform-error-list-tab"
import { PlatformRuntimeTab } from "@/features/platform/platform-runtime-tab"
import { PlatformUsageTab } from "@/features/platform/platform-usage-tab"

/** 按路由中的监控页面展示业务使用、运行状态或近期错误。 */
export function PlatformOverviewPage({ section }: { section: AdminOverviewSection }) {
  const { t } = useTranslation("platform")
  return (
    <AdminPage title={t(`overview.tabs.${section}`)} description={t(`overview.descriptions.${section}`)}>
      {section === "usage" ? (
        <PlatformUsageTab />
      ) : section === "runtime" ? (
        <PlatformRuntimeTab />
      ) : (
        <PlatformErrorListTab />
      )}
    </AdminPage>
  )
}
