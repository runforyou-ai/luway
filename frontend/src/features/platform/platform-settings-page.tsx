/** 平台设置分组的页面入口：确认平台管理员身份后渲染对应页面。 */
import type { ReactNode } from "react"

import type { Account } from "@/api"
import { PlatformAdminGate } from "@/features/platform/platform-admin-gate"
import { PlatformDeploymentPage } from "@/features/platform/platform-deployment-page"
import { PlatformLicensePage } from "@/features/platform/platform-license-page"
import { PlatformOverviewPage } from "@/features/platform/platform-overview-page"
import { PlatformWorkspacesPage } from "@/features/platform/platform-workspaces-page"

/** 平台设置分组中各页面的渲染方式。 */
const platformSections = {
  overview: () => <PlatformOverviewPage />,
  workspaces: (account: Account) => <PlatformWorkspacesPage account={account} />,
  deployment: () => <PlatformDeploymentPage />,
  license: () => <PlatformLicensePage />,
} satisfies Record<string, (account: Account) => ReactNode>

/** 平台设置分组中的页面。 */
type PlatformSettingsSection = keyof typeof platformSections

/** 按平台设置项渲染对应页面。 */
export function PlatformSettingsPage({ section }: { section: PlatformSettingsSection }) {
  return <PlatformAdminGate>{(account) => platformSections[section](account)}</PlatformAdminGate>
}
