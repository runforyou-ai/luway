/** 设置中的成员列表：邀请成员、管理待接受的邀请、停用与恢复账号，并进入编辑页。 */
import { useState } from "react"
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate } from "react-router"

import {
  InvitationStatus,
  UserStatus,
  deactivateUser,
  listInvitations,
  listRoles,
  listUsers,
  reactivateUser,
  regenerateInvitation,
  revokeInvitation,
  type Invitation,
  type InvitationCreated,
  type UserData,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import {
  ListToolbar,
  ListToolbarFilter,
  ListToolbarReset,
  ListToolbarSearch,
  ListToolbarTotal,
} from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { useWorkspace } from "@/contexts/workspace-context"
import { useAccountStatusToggle } from "@/components/account-status-toggle"
import { contactResourceKeys } from "@/hooks/use-contact-invalidator"
import { useContactSearch } from "@/hooks/use-contact-search"
import { InvitationLink, InviteMemberDialog } from "@/features/settings/members/invite-member-dialog"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useDateTime } from "@/hooks/use-date-time"
import { usePagedResource, useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { roleDisplayName } from "@/lib/role-labels"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 成员列表的状态筛选值，invited 表示待接受的邀请。 */
const invitedFilter = "invited"

/** 加载并管理企业成员账号。 */
export function MemberListPage() {
  const { t } = useTranslation("contacts")
  const { formatDateTime } = useDateTime()
  const { t: tSettings } = useTranslation("settings")
  const { t: tCommon } = useTranslation("common")
  const { identity } = useWorkspace()
  const navigate = useNavigate()
  const location = useLocation()
  const { searchParams, setParameters, query, search, setSearch } =
    useContactSearch()
  const invalidate = useResourceInvalidator()
  const [inviting, setInviting] = useState(false)
  // 编辑页返回时恢复当前筛选和滚动位置。
  const returnQuery = new URLSearchParams({
    returnTo: location.pathname + location.search,
  }).toString()
  const showInvitations = searchParams.get("status") === invitedFilter
  const status =
    optionalWailsEnum(UserStatus, searchParams.get("status")) ??
    UserStatus.UserStatusActive
  const statusFilter = showInvitations ? invitedFilter : status
  const roleId = showInvitations ? "" : searchParams.get("roleId") ?? ""
  const statusToggle = useAccountStatusToggle<UserData>({
    keyPrefix: "contacts:members.status",
    deactivate: deactivateUser,
    reactivate: reactivateUser,
    // 修改自己的账号状态时同时刷新当前身份。
    invalidateKeys: (user) => [
      ...contactResourceKeys("user", user.id),
      ...(user.id === identity.user.id ? [resourceKeys.identity()] : []),
    ],
    logLabel: "修改企业成员账号状态",
  })

  const rolesResource = useResource(resourceKeys.roles(), () => listRoles())
  const roles = rolesResource.data?.roles ?? []
  const list = usePagedResource(
    resourceKeys.users({ query, status, roleId, pageSize: 50 }),
    (page) => listUsers({ query, status, roleId, page, pageSize: 50 }),
    {
      select: (data) => ({ items: data.users, page: data.page }),
      itemKey: (user) => user.id,
      enabled: !showInvitations,
    },
  )
  const users = list.data?.items ?? []
  const invitationList = useResource(resourceKeys.invitations(), (signal) => listInvitations(signal), {
    enabled: showInvitations,
    staleTime: 0,
  })
  const normalizedQuery = query.trim().toLowerCase()
  const invitations = (invitationList.data?.items ?? []).filter(
    (invitation) =>
      !normalizedQuery ||
      invitation.email.toLowerCase().includes(normalizedQuery) ||
      invitation.displayName.toLowerCase().includes(normalizedQuery),
  )
  const [regenerated, setRegenerated] = useState<InvitationCreated | null>(null)
  const regenerate = useConfirmedAction<Invitation>({
    action: async (invitation) => setRegenerated(await regenerateInvitation(invitation.id)),
    invalidateKeys: () => [resourceKeys.invitations()],
    errorMessage: () => t("members.invitations.regenerateError"),
    logLabel: "重新生成邀请链接",
  })
  const revoke = useConfirmedAction<Invitation>({
    action: (invitation) => revokeInvitation(invitation.id),
    invalidateKeys: () => [resourceKeys.invitations()],
    successMessage: () => t("members.invitations.revoked"),
    errorMessage: () => t("members.invitations.revokeError"),
    logLabel: "撤销邀请",
  })

  const hasFilters = Boolean(statusFilter !== UserStatus.UserStatusActive || roleId)
  const roleOptions = roles.map((item) => ({
    value: item.id,
    label: roleDisplayName(item, tCommon),
  }))

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={tSettings("members.title")}
        description={tSettings("members.description")}
      >
        <Button
          variant="subtle"
          size="icon-sm"
          aria-label={t("members.invite.title")}
          title={t("members.invite.title")}
          onClick={() => setInviting(true)}
        >
          <PlusIcon />
        </Button>
      </PageHeader>

      <ListToolbar>
        <ListToolbarSearch
          value={search}
          aria-label={t("search.employees")}
          onChange={(event) => setSearch(event.target.value)}
        />
        <ListToolbarFilter
          label={t("filters.accountStatus")}
          value={statusFilter}
          options={[
            { value: UserStatus.UserStatusActive, label: t("statuses.active") },
            { value: UserStatus.UserStatusInactive, label: t("statuses.inactive") },
            { value: invitedFilter, label: t("statuses.invited") },
          ]}
          onValueChange={(next) =>
            setParameters({
              status: next === UserStatus.UserStatusActive ? null : next,
              roleId: next === invitedFilter ? null : roleId || null,
            })
          }
        />
        {showInvitations ? null : (
          <ListToolbarFilter
            label={t("filters.role")}
            allLabel={t("filters.allRoles")}
            value={roleId}
            options={roleOptions}
            contentClassName="max-h-[min(18rem,var(--radix-dropdown-menu-content-available-height))]"
            onValueChange={(value) =>
              setParameters({ roleId: value || null })
            }
          />
        )}
        {hasFilters ? (
          <ListToolbarReset
            onClick={() => setParameters({ status: null, roleId: null })}
          >
            {tCommon("actions.clearFilters")}
          </ListToolbarReset>
        ) : null}
        <ListToolbarTotal count={showInvitations ? invitationList.data ? invitations.length : undefined : list.data?.total} />
      </ListToolbar>

      {showInvitations ? (
        <ResourceListLayout resources={invitationList} errorMessage={t("members.invitations.loadError")}>
          <ResourceTable
            columns={[
              {
                key: "invitation",
                header: t("columns.employeeName"),
                cellClassName: "min-w-0",
                cell: (invitation) => (
                  <ResourceRowIdentity
                    avatar={{ name: invitation.displayName || invitation.email }}
                    name={invitation.displayName || invitation.email}
                    secondary={roleDisplayName(invitation.role, tCommon)}
                    description={invitation.displayName ? invitation.email : undefined}
                  />
                ),
              },
              {
                key: "time",
                header: tCommon("time.addedAtColumn"),
                cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
                cell: (invitation) =>
                  invitation.status === InvitationStatus.InvitationStatusExpired
                    ? t("members.invitations.expired")
                    : t("members.invitations.expiresAt", { time: formatDateTime(invitation.expiresAt) }),
              },
            ]}
            rows={invitations}
            rowKey={(invitation) => invitation.id}
            empty={t("members.invitations.empty")}
            rowActions={(invitation) => [
              {
                key: "regenerate",
                label: t("members.invitations.regenerate"),
                onSelect: () => regenerate.select(invitation),
              },
              {
                key: "revoke",
                label: t("members.invitations.revoke"),
                onSelect: () => revoke.select(invitation),
                destructive: true,
                separatorBefore: true,
              },
            ]}
          />
        </ResourceListLayout>
      ) : (
      <ResourceListLayout
        resources={list}
        errorMessage={tSettings("members.loadError")}
        more={list.more}
      >
        <ResourceTable
          columns={[
            {
              key: "member",
              header: t("columns.employeeName"),
              cellClassName: "min-w-0",
              cell: (user) => (
                <ResourceRowIdentity
                  avatar={{ imageURL: user.avatarUrl, name: user.displayName }}
                  name={user.displayName}
                  secondary={roleDisplayName(user.role, tCommon)}
                  description={user.email}
                />
              ),
            },
            {
              key: "time",
              header: tCommon("time.addedAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (user) =>
                tCommon("time.addedAt", { time: formatDateTime(user.createdAt) }),
            },
          ]}
          rows={users}
          rowKey={(user) => user.id}
          empty={tSettings("members.empty")}
          onRowActivate={(user) => navigate(`/settings/members/${user.id}?${returnQuery}`)}
          rowActions={(user) => [statusToggle.rowAction(user)]}
        />
      </ResourceListLayout>
      )}

      <ConfirmationDialog {...statusToggle.dialog} />
      <ConfirmationDialog
        {...regenerate.dialog}
        title={t("members.invitations.regenerateTitle", { email: regenerate.item?.email ?? "" })}
        description={t("members.invitations.regenerateDescription")}
        pendingLabel={t("members.invitations.regenerating")}
      />
      <ConfirmationDialog
        {...revoke.dialog}
        destructive
        title={t("members.invitations.revokeTitle", { email: revoke.item?.email ?? "" })}
        description={t("members.invitations.revokeDescription")}
        pendingLabel={t("members.invitations.revoking")}
      />
      <InviteMemberDialog
        open={inviting}
        rolesResource={rolesResource}
        onOpenChange={setInviting}
        onCreated={() => void invalidate(resourceKeys.invitations())}
      />
      <Dialog open={regenerated !== null} onOpenChange={(open) => !open && setRegenerated(null)}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>{t("members.invite.createdTitle")}</DialogTitle>
            <DialogDescription>{t("members.invitations.regeneratedDescription")}</DialogDescription>
          </DialogHeader>
          {regenerated ? <InvitationLink created={regenerated} /> : null}
        </DialogContent>
      </Dialog>
    </div>
  )
}
