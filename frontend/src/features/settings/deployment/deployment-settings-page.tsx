/** 部署设置分组的页面入口：确认部署管理员身份后渲染对应页面。 */
import { DeploymentAccountListPage } from "@/features/settings/deployment/deployment-account-list-page"
import { DeploymentAdminGate } from "@/features/settings/deployment/deployment-admin-gate"
import { DeploymentOverviewPage } from "@/features/settings/deployment/deployment-overview-page"
import { DeploymentRegistrationPage } from "@/features/settings/deployment/deployment-registration-page"
import { DeploymentRuntimePage } from "@/features/settings/deployment/deployment-runtime-page"
import { DeploymentUsagePage } from "@/features/settings/deployment/deployment-usage-page"
import { DeploymentWorkspaceListPage } from "@/features/settings/deployment/deployment-workspace-list-page"

/** 部署设置分组中的页面。 */
type DeploymentSettingsSection = "overview" | "usage" | "runtime" | "accounts" | "workspaces" | "registration"

/** 按部署设置项渲染概览、业务使用、运行状态、全部账号、全部工作区或注册与创建页面。 */
export function DeploymentSettingsPage({ section }: { section: DeploymentSettingsSection }) {
  return (
    <DeploymentAdminGate>
      {(account) =>
        section === "overview" ? (
          <DeploymentOverviewPage />
        ) : section === "usage" ? (
          <DeploymentUsagePage />
        ) : section === "runtime" ? (
          <DeploymentRuntimePage />
        ) : section === "accounts" ? (
          <DeploymentAccountListPage account={account} />
        ) : section === "workspaces" ? (
          <DeploymentWorkspaceListPage />
        ) : (
          <DeploymentRegistrationPage />
        )
      }
    </DeploymentAdminGate>
  )
}
