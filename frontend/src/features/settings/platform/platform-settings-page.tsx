/** 平台设置分组的页面入口：确认平台管理员身份后渲染对应页面。 */
import type { ReactNode } from "react"

import type { Account } from "@/api"
import { PlatformAccountListPage } from "@/features/settings/platform/platform-account-list-page"
import { PlatformAdminGate } from "@/features/settings/platform/platform-admin-gate"
import { PlatformLicensePage } from "@/features/settings/platform/platform-license-page"
import { PlatformOverviewPage } from "@/features/settings/platform/platform-overview-page"
import { PlatformModelCallListPage } from "@/features/settings/platform/platform-model-call-list-page"
import { PlatformModelFormPage } from "@/features/settings/platform/platform-model-form-page"
import { PlatformModelListPage } from "@/features/settings/platform/platform-model-list-page"
import { PlatformProviderFormPage } from "@/features/settings/platform/platform-provider-form-page"
import { PlatformProviderListPage } from "@/features/settings/platform/platform-provider-list-page"
import { PlatformRegistrationPage } from "@/features/settings/platform/platform-registration-page"
import { PlatformWorkspaceListPage } from "@/features/settings/platform/platform-workspace-list-page"

/** 平台设置分组中各页面的渲染方式。 */
const platformSections = {
  overview: () => <PlatformOverviewPage />,
  license: () => <PlatformLicensePage />,
  accounts: (account: Account) => <PlatformAccountListPage account={account} />,
  workspaces: () => <PlatformWorkspaceListPage />,
  registration: () => <PlatformRegistrationPage />,
  platformModels: () => <PlatformModelListPage />,
  platformModelCreate: () => <PlatformModelFormPage mode="create" />,
  platformModelEdit: () => <PlatformModelFormPage mode="edit" />,
  platformProviders: () => <PlatformProviderListPage />,
  platformProviderCreate: () => <PlatformProviderFormPage mode="create" />,
  platformProviderEdit: () => <PlatformProviderFormPage mode="edit" />,
  platformCalls: () => <PlatformModelCallListPage />,
} satisfies Record<string, (account: Account) => ReactNode>

/** 平台设置分组中的页面。 */
type PlatformSettingsSection = keyof typeof platformSections

/** 按平台设置项渲染对应页面。 */
export function PlatformSettingsPage({ section }: { section: PlatformSettingsSection }) {
  return <PlatformAdminGate>{(account) => platformSections[section](account)}</PlatformAdminGate>
}
