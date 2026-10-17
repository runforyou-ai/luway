/** 平台用户管理页：展示平台账号列表。 */
import { useTranslation } from "react-i18next"

import type { Account } from "@/api"
import { AdminPage } from "@/features/admin/admin-page"
import { PlatformAccountsList } from "@/features/platform/platform-accounts-list"

/** 展示平台账号列表，account 是当前平台管理员账号。 */
export function PlatformAccountsPage({ account }: { account: Account }) {
  const { t } = useTranslation("platform")
  return (
    <AdminPage title={t("accounts.pageTitle")} description={t("accounts.pageDescription")}>
      <PlatformAccountsList account={account} />
    </AdminPage>
  )
}
