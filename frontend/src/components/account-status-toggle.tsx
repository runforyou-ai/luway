/** 企业成员与 AI 员工列表共用的账号状态筛选、启停操作和确认流程。 */
import type { QueryKey } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import { UserStatus } from "@/api"
import { ListToolbarFilter } from "@/components/list-toolbar"
import type { ResourceRowAction } from "@/components/resource-table"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"

/** 启停文案所在的词条前缀。 */
type AccountStatusKeyPrefix = "contacts:members.status" | "contacts:assistants.status" | "agents:status"

type AccountItem = {
  id: string
  status: UserStatus
  displayName: string
}

/** 按账号状态筛选列表，默认显示正常账号。 */
export function AccountStatusFilter({
  value,
  setParameters,
}: {
  value: UserStatus
  setParameters: (changes: Record<string, string | null>) => void
}) {
  const { t } = useTranslation("contacts")
  return (
    <ListToolbarFilter
      label={t("filters.accountStatus")}
      value={value}
      options={[
        { value: UserStatus.UserStatusActive, label: t("statuses.active") },
        { value: UserStatus.UserStatusInactive, label: t("statuses.inactive") },
      ]}
      onValueChange={(next) =>
        setParameters({
          status: next === UserStatus.UserStatusActive ? null : next,
          selected: null,
        })
      }
    />
  )
}

/** 确认后禁用正常账号或恢复已禁用账号，返回行操作菜单项和确认框属性。 */
export function useAccountStatusToggle<T extends AccountItem>({
  keyPrefix,
  deactivate,
  reactivate,
  invalidateKeys,
  logLabel,
}: {
  keyPrefix: AccountStatusKeyPrefix
  deactivate: (id: string) => Promise<unknown>
  reactivate: (id: string) => Promise<unknown>
  invalidateKeys: (item: T) => QueryKey[]
  logLabel: string
}) {
  const { t } = useTranslation(["contacts", "agents"])
  const action = useConfirmedAction<T>({
    action: (item) =>
      item.status === UserStatus.UserStatusActive
        ? deactivate(item.id)
        : reactivate(item.id),
    invalidateKeys,
    successMessage: (item) =>
      t(
        item.status === UserStatus.UserStatusActive
          ? `${keyPrefix}.deactivated`
          : `${keyPrefix}.reactivated`,
      ),
    errorMessage: () => t(`${keyPrefix}.error`),
    logLabel,
  })
  const deactivating = action.item?.status === UserStatus.UserStatusActive

  return {
    /** 返回列表行的启停菜单项：正常账号为危险的禁用操作，已禁用账号为恢复正常。 */
    rowAction: (item: T): ResourceRowAction => {
      const active = item.status === UserStatus.UserStatusActive
      return {
        key: "status",
        label: t(active ? `${keyPrefix}.deactivate` : `${keyPrefix}.reactivate`),
        onSelect: () => action.select(item),
        destructive: active,
        separatorBefore: active,
      }
    },
    dialog: {
      open: action.item !== null,
      pending: action.pending,
      title: t(
        deactivating ? `${keyPrefix}.deactivateTitle` : `${keyPrefix}.reactivateTitle`,
        { name: action.item?.displayName ?? "" },
      ),
      description: t(
        deactivating
          ? `${keyPrefix}.deactivateDescription`
          : `${keyPrefix}.reactivateDescription`,
      ),
      destructive: deactivating,
      pendingLabel: t(`${keyPrefix}.saving`),
      onOpenChange: (open: boolean) => {
        if (!open) action.select(null)
      },
      onConfirm: () => void action.confirm(),
    },
  }
}
