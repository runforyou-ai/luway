/** 平台用户管理列表：搜索和筛选平台账号，管理账号状态与平台管理员身份，按页码翻页。 */
import { useTranslation } from "react-i18next"

import {
  AccountStatus,
  deactivatePlatformAccount,
  grantPlatformAdmin,
  listPlatformAccounts,
  reactivatePlatformAccount,
  revokePlatformAdmin,
  type Account,
  type PlatformAccount,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ListToolbar, ListToolbarFilter, ListToolbarSearch } from "@/components/list-toolbar"
import { ResourceTable, type ResourceRowAction } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { AdminListLayout, AdminTableFrame } from "@/features/admin/admin-list-layout"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useAdminPageQuery } from "@/hooks/use-admin-page-query"
import { useDateTime } from "@/hooks/use-date-time"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { parseEnum } from "@/lib/enum"

/** 账号行上需要确认的修改。 */
type AccountChange = "deactivate" | "reactivate" | "grantAdmin" | "revokeAdmin"

/** 待确认的账号与修改。 */
type PendingAccountChange = {
  account: PlatformAccount
  change: AccountChange
}

/** 各修改对应的请求。 */
const accountChangeRequests: Record<AccountChange, (accountId: string) => Promise<PlatformAccount>> = {
  deactivate: deactivatePlatformAccount,
  reactivate: reactivatePlatformAccount,
  grantAdmin: grantPlatformAdmin,
  revokeAdmin: revokePlatformAdmin,
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

/** 列出平台账号，筛选与页码同步到地址；其他账号的修改经确认后执行。 */
export function PlatformAccountsList({ account }: { account: Account }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const { searchParams, setParameters, query, search, setSearch } = useListSearchParams({ resetParameter: "page" })
  const status = parseEnum(AccountStatus, searchParams.get("status")) ?? AccountStatus.Active
  const list = useAdminPageQuery(
    resourceKeys.platformAccounts({ query, status }),
    ({ page, pageSize }, signal) => listPlatformAccounts({ query, status, page, pageSize }, signal),
  )
  const pending = useConfirmedAction<PendingAccountChange>({
    action: ({ account: target, change }) => accountChangeRequests[change](target.id),
    invalidateKeys: () => [resourceKeys.platformAccounts(), resourceKeys.platformOverview(), resourceKeys.platformWorkspaces()],
    successMessage: ({ change }) => t(accountChangeMessages[change]),
    errorMessage: () => t("accounts.updateError"),
    logLabel: "修改平台账号",
  })
  const dialog = pending.item ? accountChangeDialogs[pending.item.change] : null

  /** 常用状态/权限操作放到操作列，危险操作放进「⋯」。 */
  function rowActions(item: PlatformAccount): ResourceRowAction[] {
    if (item.id === account.id) return []
    const action = (change: AccountChange, primary: boolean) => ({
      key: change,
      label: t(`accounts.${change}`),
      onSelect: () => pending.select({ account: item, change }),
      destructive: change === "revokeAdmin" || change === "deactivate",
      primary,
    })
    const roleChange: AccountChange = item.isPlatformAdmin ? "revokeAdmin" : "grantAdmin"
    const statusChange: AccountChange = item.status === AccountStatus.Active ? "deactivate" : "reactivate"
    return [
      action(roleChange, roleChange === "grantAdmin"),
      action(statusChange, statusChange === "reactivate"),
    ]
  }

  return (
    <>
      <ListToolbar>
        <ListToolbarSearch value={search} aria-label={t("accounts.search")} onChange={(event) => setSearch(event.target.value)} />
        <ListToolbarFilter
          label={t("accounts.statusFilter")}
          value={status}
          options={[
            { value: AccountStatus.Active, label: t("accounts.statuses.active") },
            { value: AccountStatus.Inactive, label: t("accounts.statuses.inactive") },
          ]}
          onValueChange={(next) => setParameters({ status: next === AccountStatus.Active ? null : next, page: null })}
        />
      </ListToolbar>

      <AdminListLayout
        resources={list}
        errorMessage={t("accounts.loadError")}
        pagination={{
          page: list.data?.page,
          onPageChange: list.setPage,
          onPageSizeChange: list.setPageSize,
          busy: list.isPlaceholderData,
        }}
      >
        <AdminTableFrame>
          <ResourceTable<PlatformAccount>
            columns={[
              {
                key: "name",
                header: t("accounts.nameColumn"),
                cellClassName: "min-w-[8rem] whitespace-nowrap font-medium",
                cell: (item) => (
                  <span className="inline-flex items-center gap-2">
                    {item.displayName}
                    {item.id === account.id ? <StatusBadge variant="muted">{t("accounts.you")}</StatusBadge> : null}
                  </span>
                ),
              },
              {
                key: "email",
                header: t("accounts.emailColumn"),
                cellClassName: "min-w-[12rem] whitespace-nowrap text-muted-foreground",
                cell: (item) => item.email,
              },
              {
                key: "role",
                header: t("accounts.roleColumn"),
                cellClassName: "w-px whitespace-nowrap",
                cell: (item) =>
                  item.isPlatformAdmin ? (
                    <StatusBadge variant="muted">{t("accounts.platformAdmin")}</StatusBadge>
                  ) : (
                    <span className="text-muted-foreground">{t("accounts.member")}</span>
                  ),
              },
              {
                key: "workspaces",
                header: t("accounts.workspacesColumn"),
                cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
                cell: (item) => item.workspaceCount,
              },
              {
                key: "time",
                header: t("accounts.registeredAtColumn"),
                cellClassName: "w-px whitespace-nowrap text-muted-foreground",
                cell: (item) => formatDateTime(item.createdAt),
              },
            ]}
            rows={list.data?.accounts ?? []}
            rowKey={(item) => item.id}
            empty={t("accounts.empty")}
            rowActions={rowActions}
            showHeader
          />
        </AdminTableFrame>
      </AdminListLayout>

      <ConfirmationDialog
        {...pending.dialog}
        destructive={pending.item?.change === "deactivate" || pending.item?.change === "revokeAdmin"}
        title={dialog ? t(dialog.title, { name: pending.item?.account.displayName ?? "" }) : ""}
        description={dialog ? t(dialog.description) : ""}
        pendingLabel={t("accounts.saving")}
      />
    </>
  )
}
