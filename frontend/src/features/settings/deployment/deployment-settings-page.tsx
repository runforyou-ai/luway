/** 部署设置分组的页面入口：确认部署管理员身份后渲染对应页面。 */
import type { ReactNode } from "react"

import type { Account } from "@/api"
import { DeploymentAccountListPage } from "@/features/settings/deployment/deployment-account-list-page"
import { DeploymentAdminGate } from "@/features/settings/deployment/deployment-admin-gate"
import { DeploymentLicensePage } from "@/features/settings/deployment/deployment-license-page"
import { DeploymentOverviewPage } from "@/features/settings/deployment/deployment-overview-page"
import { DeploymentPlatformModelCallListPage } from "@/features/settings/deployment/deployment-platform-model-call-list-page"
import { DeploymentPlatformModelFormPage } from "@/features/settings/deployment/deployment-platform-model-form-page"
import { DeploymentPlatformModelListPage } from "@/features/settings/deployment/deployment-platform-model-list-page"
import { DeploymentPlatformProviderFormPage } from "@/features/settings/deployment/deployment-platform-provider-form-page"
import { DeploymentPlatformProviderListPage } from "@/features/settings/deployment/deployment-platform-provider-list-page"
import { DeploymentRegistrationPage } from "@/features/settings/deployment/deployment-registration-page"
import { DeploymentRuntimePage } from "@/features/settings/deployment/deployment-runtime-page"
import { DeploymentUsagePage } from "@/features/settings/deployment/deployment-usage-page"
import { DeploymentWorkspaceListPage } from "@/features/settings/deployment/deployment-workspace-list-page"

/** 部署设置分组中各页面的渲染方式。 */
const deploymentSections = {
  overview: () => <DeploymentOverviewPage />,
  usage: () => <DeploymentUsagePage />,
  runtime: () => <DeploymentRuntimePage />,
  license: () => <DeploymentLicensePage />,
  accounts: (account: Account) => <DeploymentAccountListPage account={account} />,
  workspaces: () => <DeploymentWorkspaceListPage />,
  registration: () => <DeploymentRegistrationPage />,
  platformModels: () => <DeploymentPlatformModelListPage />,
  platformModelCreate: () => <DeploymentPlatformModelFormPage mode="create" />,
  platformModelEdit: () => <DeploymentPlatformModelFormPage mode="edit" />,
  platformProviders: () => <DeploymentPlatformProviderListPage />,
  platformProviderCreate: () => <DeploymentPlatformProviderFormPage mode="create" />,
  platformProviderEdit: () => <DeploymentPlatformProviderFormPage mode="edit" />,
  platformCalls: () => <DeploymentPlatformModelCallListPage />,
} satisfies Record<string, (account: Account) => ReactNode>

/** 部署设置分组中的页面。 */
type DeploymentSettingsSection = keyof typeof deploymentSections

/** 按部署设置项渲染对应页面。 */
export function DeploymentSettingsPage({ section }: { section: DeploymentSettingsSection }) {
  return <DeploymentAdminGate>{(account) => deploymentSections[section](account)}</DeploymentAdminGate>
}
