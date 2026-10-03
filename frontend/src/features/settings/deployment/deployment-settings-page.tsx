/** 部署设置分组的页面入口：确认部署管理员身份后渲染对应页面。 */
import { DeploymentAccountListPage } from "@/features/settings/deployment/deployment-account-list-page"
import { DeploymentAdminGate } from "@/features/settings/deployment/deployment-admin-gate"
import { DeploymentLicensePage } from "@/features/settings/deployment/deployment-license-page"
import { DeploymentOverviewPage } from "@/features/settings/deployment/deployment-overview-page"
import { DeploymentRegistrationPage } from "@/features/settings/deployment/deployment-registration-page"
import { DeploymentWorkspaceListPage } from "@/features/settings/deployment/deployment-workspace-list-page"

/** 部署设置分组中的页面。 */
type DeploymentSettingsSection = "overview" | "license" | "accounts" | "workspaces" | "registration"

/** 按部署设置项渲染概览、实例授权、全部账号、全部工作区或注册与创建页面。 */
export function DeploymentSettingsPage({ section }: { section: DeploymentSettingsSection }) {
  return (
    <DeploymentAdminGate>
      {(account) =>
        section === "overview" ? (
          <DeploymentOverviewPage />
        ) : section === "license" ? (
          <DeploymentLicensePage />
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
