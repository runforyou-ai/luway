/** 角色与权限列表页。 */
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate } from "react-router"

import {
  deleteRole,
  listRoles,
  RoleKind,
  type PermissionDefinition,
  type RoleData,
} from "@/api"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceTable } from "@/components/resource-table"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { Button } from "@/components/ui/button"
import {
  permissionDefinitionLabel,
  roleDescription,
  roleDisplayName,
} from "@/lib/role-labels"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useResource } from "@/hooks/use-resource"

/** 显示角色已配置的权限名称和总数。 */
function permissionSummary(
  role: RoleData,
  definitions: PermissionDefinition[],
  t: ReturnType<typeof useTranslation<"settings">>["t"],
) {
  const selected = new Set(role.permissions)
  const labels = definitions
    .filter((item) => selected.has(item.code))
    .map((item) => permissionDefinitionLabel(item, t))
  if (labels.length === 0) return t("roles.list.permissionEmpty")
  const items = labels.slice(0, 2).join(t("roles.list.permissionSeparator"))
  return labels.length > 2
    ? t("roles.list.permissionSummary", { items, count: labels.length })
    : items
}

/** 加载并管理企业角色列表。 */
export function RoleListPage() {
  const { t } = useTranslation("settings")
  const { t: tCommon } = useTranslation("common")
  const navigate = useNavigate()
  const resource = useResource(resourceKeys.roles(), () => listRoles())
  const { data } = resource
  const roles = data?.roles ?? []
  const permissions = data?.permissions ?? []
  const maximum = data?.maximum ?? null

  const deletion = useConfirmedAction<RoleData>({
    action: (role) => deleteRole(role.id),
    invalidateKeys: (role) => [resourceKeys.roles(), resourceKeys.role(role.id)],
    logLabel: "删除角色",
    successMessage: () => t("roles.delete.success"),
    errorMessage: () => t("roles.delete.error"),
  })

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={t("roles.title")}
        description={t("roles.description")}
      >
        {maximum !== null && roles.length < maximum ? (
          <Button variant="subtle" size="icon-sm" asChild>
            <Link
              to="/settings/roles/new"
              aria-label={t("roles.list.create")}
              title={t("roles.list.create")}
            >
              <PlusIcon />
            </Link>
          </Button>
        ) : (
          <Button
            variant="subtle"
            size="icon-sm"
            disabled
            aria-label={t("roles.list.create")}
            title={
              maximum === null
                ? t("roles.list.create")
                : t("roles.list.limitReached")
            }
          >
            <PlusIcon />
          </Button>
        )}
      </PageHeader>
      <ResourceListLayout
        resources={resource}
        errorMessage={t("roles.list.loadError")}
        frameClassName="@container"
      >
        <ResourceTable
          columns={[
            {
              key: "role",
              header: t("roles.list.columns.name"),
              cell: (role) => {
                const description = roleDescription(role, t)
                return (
                  <div className="min-w-0">
                    <p className="truncate font-medium">
                      {roleDisplayName(role, tCommon)}
                    </p>
                    <p className="truncate text-xs text-muted-foreground">
                      {t("roles.list.memberCount", { count: role.memberCount })}
                      <span aria-hidden="true"> · </span>
                      {description}
                    </p>
                  </div>
                )
              },
            },
            {
              key: "permissions",
              header: t("roles.list.columns.permissions"),
              className: "hidden @3xl:table-cell",
              cellClassName: "text-muted-foreground",
              cell: (role) => permissionSummary(role, permissions, t),
            },
          ]}
          rows={roles}
          rowKey={(role) => role.id}
          empty={t("roles.list.empty")}
          onRowActivate={(role) => navigate(`/settings/roles/${role.id}`)}
          // 内置角色不可删除。
          rowActions={(role) =>
            role.kind === RoleKind.RoleKindCustom
              ? [
                  {
                    key: "delete",
                    label: tCommon("actions.delete"),
                    destructive: true,
                    separatorBefore: true,
                    onSelect: () => deletion.select(role),
                  },
                ]
              : []
          }
        />
      </ResourceListLayout>

      <ConfirmationDialog
        {...deletion.dialog}
        title={
          deletion.item
            ? t("roles.delete.title", {
                name: roleDisplayName(deletion.item, tCommon),
              })
            : ""
        }
        description={t("roles.delete.description")}
        pendingLabel={tCommon("actions.deleting")}
      />
    </div>
  )
}
