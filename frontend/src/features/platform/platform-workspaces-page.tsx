/** 平台工作区管理页：展示工作区列表。 */
import { useTranslation } from "react-i18next"

import type { Account } from "@/api"
import { AdminPage } from "@/features/admin/admin-page"
import { PlatformWorkspaceList } from "@/features/platform/platform-workspace-list"

/** 展示工作区列表，account 是当前平台管理员账号。 */
export function PlatformWorkspacesPage({ account }: { account: Account }) {
  const { t } = useTranslation("platform")
  return (
    <AdminPage title={t("workspacesAndAccounts.title")} description={t("workspacesAndAccounts.description")}>
      <PlatformWorkspaceList account={account} />
    </AdminPage>
  )
}
