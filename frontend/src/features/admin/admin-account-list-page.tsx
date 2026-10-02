/** 部署管理的账号页：按状态筛选和搜索部署内账号，停用或恢复账号，授予或撤销部署管理员。 */
import { useTranslation } from "react-i18next"

import {
  AccountStatus,
  deactivateDeploymentAccount,
  grantDeploymentAdmin,
  listDeploymentAccounts,
  reactivateDeploymentAccount,
  revokeDeploymentAdmin,
  type Account,
  type DeploymentAccount,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ListToolbar, ListToolbarFilter, ListToolbarSearch, ListToolbarTotal } from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable, type ResourceRowAction } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useDateTime } from "@/hooks/use-date-time"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { usePagedResource } from "@/hooks/use-resource"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 账号行上需要确认的修改。 */
type AccountChange = "deactivate" | "reactivate" | "grantAdmin" | "revokeAdmin"

/** 待确认的账号与修改。 */
type PendingAccountChange = {
  account: DeploymentAccount
  change: AccountChange
}

/** 各修改对应的请求。 */
const accountChangeRequests: Record<AccountChange, (accountId: string) => Promise<DeploymentAccount>> = {
  deactivate: deactivateDeploymentAccount,
  reactivate: reactivateDeploymentAccount,
  grantAdmin: grantDeploymentAdmin,
  revokeAdmin: revokeDeploymentAdmin,
}

/** 各修改对应的成功提示词条。 */
const accountChangeMessages = {
  deactivate: "accounts.deactivated",
  reactivate: "accounts.reactivated",
  grantAdmin: "accounts.granted",
  revokeAdmin: "accounts.revoked",
} as const

/** 各修改对应的确认标题与说明词条。 */
const accountChangeDialogs = {
  deactivate: { title: "accounts.deactivateTitle", description: "accounts.deactivateDescription" },
  reactivate: { title: "accounts.reactivateTitle", description: "accounts.reactivateDescription" },
  grantAdmin: { title: "accounts.grantAdminTitle", description: "accounts.grantAdminDescription" },
  revokeAdmin: { title: "accounts.revokeAdminTitle", description: "accounts.revokeAdminDescription" },
} as const

/** 列出部署账号；当前账号只展示，其他账号的修改经确认后执行。 */
export function AdminAccountListPage({ account }: { account: Account }) {
  const { t } = useTranslation("admin")
  const { formatDateTime } = useDateTime()
  const { searchParams, setParameters, query, search, setSearch } = useListSearchParams()
  const status = optionalWailsEnum(AccountStatus, searchParams.get("status")) ?? AccountStatus.AccountStatusActive
  const list = usePagedResource(
    resourceKeys.deploymentAccounts({ query, status, pageSize: 50 }),
    (page, signal) => listDeploymentAccounts({ query, status, page, pageSize: 50 }, signal),
    { select: (data) => ({ items: data.accounts, page: data.page }), itemKey: (item) => item.id },
  )
  const pending = useConfirmedAction<PendingAccountChange>({
    action: ({ account: target, change }) => accountChangeRequests[change](target.id),
    invalidateKeys: () => [resourceKeys.deploymentAccounts(), resourceKeys.deploymentOverview()],
    successMessage: ({ change }) => t(accountChangeMessages[change]),
    errorMessage: () => t("accounts.updateError"),
    logLabel: "修改部署账号",
  })
  const dialog = pending.item ? accountChangeDialogs[pending.item.change] : null

  /** 返回其他账号的行操作：授予管理员与恢复在前，撤销管理员与停用作为危险操作放在分隔线之后。 */
  function rowActions(item: DeploymentAccount): ResourceRowAction[] {
    if (item.id === account.id) return []
    const action = (change: AccountChange) => ({
      key: change,
      label: t(`accounts.${change}`),
      onSelect: () => pending.select({ account: item, change }),
      destructive: change === "revokeAdmin" || change === "deactivate",
    })
    const changes: AccountChange[] = [
      item.isDeploymentAdmin ? "revokeAdmin" : "grantAdmin",
      item.status === AccountStatus.AccountStatusActive ? "deactivate" : "reactivate",
    ]
    const safe = changes.map(action).filter((entry) => !entry.destructive)
    const destructive = changes.map(action).filter((entry) => entry.destructive)
    return [...safe, ...destructive.map((entry, index) => ({ ...entry, separatorBefore: index === 0 && safe.length > 0 }))]
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("accounts.title")} description={t("accounts.description")} />

      <ListToolbar>
        <ListToolbarSearch value={search} aria-label={t("accounts.search")} onChange={(event) => setSearch(event.target.value)} />
        <ListToolbarFilter
          label={t("accounts.statusFilter")}
          value={status}
          options={[
            { value: AccountStatus.AccountStatusActive, label: t("accounts.statuses.active") },
            { value: AccountStatus.AccountStatusInactive, label: t("accounts.statuses.inactive") },
          ]}
          onValueChange={(next) => setParameters({ status: next === AccountStatus.AccountStatusActive ? null : next })}
        />
        <ListToolbarTotal count={list.data?.total} />
      </ListToolbar>

      <ResourceListLayout resources={list} errorMessage={t("accounts.loadError")} more={list.more}>
        <ResourceTable<DeploymentAccount>
          columns={[
            {
              key: "account",
              header: t("accounts.title"),
              cellClassName: "min-w-0",
              cell: (item) => (
                <ResourceRowIdentity
                  avatar={{ name: item.displayName }}
                  name={item.displayName}
                  secondary={item.isDeploymentAdmin ? t("accounts.deploymentAdmin") : undefined}
                  badge={item.id === account.id ? <StatusBadge variant="muted">{t("accounts.you")}</StatusBadge> : undefined}
                  description={item.email}
                />
              ),
            },
            {
              key: "workspaces",
              header: t("navigation.workspaces"),
              cellClassName: "hidden w-px whitespace-nowrap text-muted-foreground sm:table-cell",
              cell: (item) =>
                item.workspaceCount > 0 ? t("accounts.workspaceCount", { count: item.workspaceCount }) : t("accounts.noWorkspace"),
            },
            {
              key: "time",
              header: t("accounts.registeredAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (item) => t("accounts.registeredAt", { time: formatDateTime(item.createdAt) }),
            },
          ]}
          rows={list.data?.items ?? []}
          rowKey={(item) => item.id}
          empty={t("accounts.empty")}
          rowActions={rowActions}
        />
      </ResourceListLayout>

      <ConfirmationDialog
        {...pending.dialog}
        destructive={pending.item?.change === "deactivate" || pending.item?.change === "revokeAdmin"}
        title={dialog ? t(dialog.title, { name: pending.item?.account.displayName ?? "" }) : ""}
        description={dialog ? t(dialog.description) : ""}
        pendingLabel={t("accounts.saving")}
      />
    </div>
  )
}
