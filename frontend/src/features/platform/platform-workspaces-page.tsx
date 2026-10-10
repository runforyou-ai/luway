/** 平台设置的工作区与账号页：按页签管理工作区、账号与注册和创建策略。 */
import { useTranslation } from "react-i18next"

import type { Account } from "@/api"
import { PlatformAccountListTab } from "@/features/platform/platform-account-list-tab"
import { PlatformRegistrationTab } from "@/features/platform/platform-registration-tab"
import { PlatformTabsPage } from "@/features/platform/platform-tabs"
import { PlatformWorkspaceListTab } from "@/features/platform/platform-workspace-list-tab"

/** 按页签渲染工作区列表、账号列表或注册与创建设置，account 是当前平台管理员账号。 */
export function PlatformWorkspacesPage({ account }: { account: Account }) {
  const { t } = useTranslation("platform")
  return (
    <PlatformTabsPage
      title={t("workspacesAndAccounts.title")}
      description={t("workspacesAndAccounts.description")}
      tabs={[
        { value: "workspaces", label: t("workspacesAndAccounts.tabs.workspaces") },
        { value: "accounts", label: t("workspacesAndAccounts.tabs.accounts") },
        { value: "registration", label: t("workspacesAndAccounts.tabs.registration") },
      ]}
    >
      {(tab) =>
        tab === "workspaces" ? (
          <PlatformWorkspaceListTab />
        ) : tab === "accounts" ? (
          <PlatformAccountListTab account={account} />
        ) : (
          <PlatformRegistrationTab />
        )
      }
    </PlatformTabsPage>
  )
}
