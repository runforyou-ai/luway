/** 单个团队的成员列表与批量管理面板。 */
import { useEffect, useState } from "react"
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import {
  OrganizationIdentityType,
  WorkStatus,
  deleteTeam,
  getTeam,
  isNotFoundApiError,
  listTeamMembers,
  removeTeamMembers,
  type Team,
  type TeamMember,
} from "@/api"
import {
  ListToolbarFilter,
  ListToolbarReset,
  ListToolbarSearch,
  ListToolbarTotal,
} from "@/components/list-toolbar"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { UnsavedDialog } from "@/components/unsaved-dialog"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { WorkStatusDot, workStatusLabel } from "@/components/work-status"
import { useWorkspace } from "@/contexts/workspace-context"
import { ContactListSection } from "@/features/contacts/contact-list-section"
import { TeamForm } from "@/features/contacts/teams/team-form"
import { TeamMemberPicker } from "@/features/contacts/teams/team-member-picker"
import { teamMembershipCacheKeys } from "@/features/contacts/teams/team-membership-cache"
import { useContactSearch } from "@/hooks/use-contact-search"
import { useDateTime } from "@/hooks/use-date-time"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { usePagedResource, useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { useReturnLink, useReturnTo } from "@/hooks/use-return-to"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 单个团队的成员列表、批量移出和添加成员弹窗。 */
export function TeamPanel({ teamId }: { teamId: string }) {
  const { t } = useTranslation("contacts")
  const { t: tCommon } = useTranslation("common")
  const { identity } = useWorkspace()
  const navigate = useNavigate()
  const { formatDateTime } = useDateTime()
  const invalidate = useResourceInvalidator()
  const {
    searchParams,
    setParameters,
    query,
    search,
    setSearch,
  } = useContactSearch()
  const workStatus = optionalWailsEnum(
    WorkStatus,
    searchParams.get("workStatus"),
  )
  const addingTeamMembers = searchParams.get("addMembers") === "1"
  const teamResource = useResource(resourceKeys.team(teamId), () =>
    getTeam(teamId),
  )
  const selectedTeam = teamResource.data
  // 返回来源团队列表并保留其搜索与滚动位置。
  const { returnTo, leave } = useReturnTo("/contacts/teams", {
    notFound: isNotFoundApiError(teamResource.error),
    logFields: { team_id: teamId },
  })
  const [editingTeam, setEditingTeam] = useState(false)
  // 删除团队后回到来源列表。
  const teamDeletion = useConfirmedAction<Team>({
    action: (team) => deleteTeam(team.id),
    invalidateKeys: () => [resourceKeys.teams(), resourceKeys.serviceCategories(), ...teamMembershipCacheKeys],
    successMessage: () => t("teams.delete.success"),
    errorMessage: () => t("teams.delete.error"),
    logLabel: "删除团队",
    onSuccess: () => leave({ replace: true }),
  })
  const returnLink = useReturnLink()
  const [selectedTeamMemberIdentityIDs, setSelectedTeamMemberIdentityIDs] =
    useState<Set<string>>(new Set())

  const list = usePagedResource(
    resourceKeys.teamMembers(teamId, { query, workStatus, pageSize: 50 }),
    (page) => listTeamMembers(teamId, { query, workStatus, page, pageSize: 50 }),
    { select: (data) => ({ items: data.members, page: data.page }), itemKey: (member) => member.identityId },
  )
  const teamMembers = list.data?.items ?? []

  useEffect(() => {
    setSelectedTeamMemberIdentityIDs(new Set())
  }, [query, teamId, workStatus])


  /** 返回团队成员行显示的工作状态。 */
  function identityWorkStatus(member: TeamMember) {
    return member.identityType ===
      OrganizationIdentityType.OrganizationIdentityTypeUser &&
      member.identityId === identity.user.identityId
      ? identity.user.workStatus
      : member.workStatus
  }

  const memberRemoval = useConfirmedAction<TeamMember[]>({
    action: (members) =>
      removeTeamMembers(teamId, {
        members: members.map((member) => ({
          identityType: member.identityType,
          identityId: member.identityId,
        })),
      }),
    invalidateKeys: () => [
      resourceKeys.teams(),
      resourceKeys.teamMembers(),
      resourceKeys.teamMemberCandidates(teamId),
      ...teamMembershipCacheKeys,
    ],
    successMessage: (members) =>
      t(
        members.length === 1
          ? "teams.members.removed"
          : "teams.members.removedMultiple",
        { count: members.length },
      ),
    errorMessage: () => t("teams.members.removeError"),
    logLabel: "移出团队成员",
    onSuccess: () => setSelectedTeamMemberIdentityIDs(new Set()),
  })
  const removingTeamMembers = memberRemoval.item ?? []

  /** 切换已加载的全部团队成员的选中状态。 */
  function toggleAllVisibleTeamMembers(checked: boolean) {
    setSelectedTeamMemberIdentityIDs(
      checked
        ? new Set(teamMembers.map((member) => member.identityId))
        : new Set(),
    )
  }

  /** 切换单个团队成员的选中状态。 */
  function toggleTeamMember(identityID: string, checked: boolean) {
    setSelectedTeamMemberIdentityIDs((current) => {
      const next = new Set(current)
      if (checked) {
        next.add(identityID)
      } else {
        next.delete(identityID)
      }
      return next
    })
  }

  const hasInternalFilters = Boolean(workStatus)
  const allVisibleTeamMembersSelected =
    teamMembers.length > 0 &&
    teamMembers.every((member) =>
      selectedTeamMemberIdentityIDs.has(member.identityId),
    )

  return (
    <>
      <ContactListSection
        title={selectedTeam?.name ?? t("scopes.teams")}
        description={t("scopeDescriptions.teams")}
        scope="team"
        backTo={returnTo}
        headerActions={
          <>
            {selectedTeam ? (
            <>
              {selectedTeamMemberIdentityIDs.size > 0 ? (
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() =>
                    memberRemoval.select(
                      teamMembers.filter((member) =>
                        selectedTeamMemberIdentityIDs.has(member.identityId),
                      ),
                    )
                  }
                >
                  {t("teams.members.removeSelected", {
                    count: selectedTeamMemberIdentityIDs.size,
                  })}
                </Button>
              ) : null}
              <Button variant="outline" size="sm" onClick={() => setEditingTeam(true)}>
                {tCommon("actions.edit")}
              </Button>
              <Button variant="outline" size="sm" onClick={() => teamDeletion.select(selectedTeam)}>
                {tCommon("actions.delete")}
              </Button>
              <Button
                variant="subtle"
                size="icon-sm"
                className="shrink-0"
                aria-label={t("teams.members.add")}
                title={t("teams.members.add")}
                onClick={() => setParameters({ addMembers: "1" })}
              >
                <PlusIcon />
              </Button>
            </>
            ) : null}
          </>
        }
        toolbar={
          <>
            {selectedTeam ? (
              <label className="flex h-9 items-center gap-2 px-1 text-sm">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  disabled={teamMembers.length === 0}
                  checked={allVisibleTeamMembersSelected}
                  onChange={(event) =>
                    toggleAllVisibleTeamMembers(event.target.checked)
                  }
                />
                {tCommon("actions.selectAll")}
              </label>
            ) : null}
            <ListToolbarSearch
              value={search}
              aria-label={t("search.teamMembers")}
              onChange={(event) => setSearch(event.target.value)}
            />
            <ListToolbarFilter
              label={t("filters.workStatus")}
              allLabel={t("filters.allWorkStatuses")}
              value={workStatus ?? ""}
              options={[
                {
                  value: WorkStatus.WorkStatusWorking,
                  label: workStatusLabel(WorkStatus.WorkStatusWorking, tCommon),
                },
                {
                  value: WorkStatus.WorkStatusAway,
                  label: workStatusLabel(WorkStatus.WorkStatusAway, tCommon),
                },
                {
                  value: WorkStatus.WorkStatusOffDuty,
                  label: workStatusLabel(WorkStatus.WorkStatusOffDuty, tCommon),
                },
              ]}
              onValueChange={(value) =>
                setParameters({
                  workStatus: value || null,
                })
              }
            />
            {hasInternalFilters ? (
              <ListToolbarReset
                onClick={() =>
                  setParameters({
                    workStatus: null,
                  })
                }
              >
                {tCommon("actions.clearFilters")}
              </ListToolbarReset>
            ) : null}
            <ListToolbarTotal count={list.data?.total} />
          </>
        }
        list={list}
        more={list.more}
      >
        <ResourceTable
          columns={[
            ...(selectedTeam ? [{
              key: "select",
              header: null,
              cellClassName: "w-10",
              cell: (member) => (
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  aria-label={t("teams.members.selectMember", {
                    name: member.displayName,
                  })}
                  checked={selectedTeamMemberIdentityIDs.has(
                    member.identityId,
                  )}
                  onClick={(event) => event.stopPropagation()}
                  onChange={(event) =>
                    toggleTeamMember(member.identityId, event.target.checked)
                  }
                />
              ),
            }] : []),
            {
              key: "memberName",
              header: t("columns.memberName"),
              cell: (member) => {
                const agent =
                  member.identityType ===
                  OrganizationIdentityType.OrganizationIdentityTypeAgent
                return (
                  <ResourceRowIdentity
                    avatar={{
                      imageURL: member.avatarUrl,
                      name: member.displayName,
                      fallback: agent ? "agent" : "person",
                    }}
                    mark={<WorkStatusDot status={identityWorkStatus(member)} />}
                    name={member.displayName}
                    secondary={t(
                      agent ? "identityCategories.agent" : "identityCategories.user",
                    )}
                  />
                )
              },
            },
            {
              key: "joinedAt",
              header: t("columns.joinedAt"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (member) =>
                t("teams.members.joinedAt", {
                  time: formatDateTime(member.joinedAt),
                }),
            },
          ]}
          rows={teamMembers}
          rowKey={(member) => member.identityId}
          // 同事与同事列表一样进入单聊，AI 员工进入其编辑页并可返回本团队。
          onRowActivate={(member) =>
            navigate(
              member.identityType === OrganizationIdentityType.OrganizationIdentityTypeAgent
                ? returnLink(`/ai-employees/${member.agentId}`)
                : `/chats?target=${member.identityId}`,
            )
          }
          // 自己的行不可进入。
          canActivateRow={(member) => member.identityId !== identity.user.identityId}
          empty={t("list.empty")}
          rowActions={
            selectedTeam
              ? (member) => [
                  {
                    key: "remove",
                    label: t("teams.members.remove"),
                    destructive: true,
                    separatorBefore: true,
                    onSelect: () => memberRemoval.select([member]),
                  },
                ]
              : undefined
          }
        />
      </ContactListSection>

      {selectedTeam ? (
        <Dialog
          open={addingTeamMembers}
          onOpenChange={(open) => !open && setParameters({ addMembers: null })}
        >
          <DialogContent className="max-w-2xl">
            <DialogHeader>
              <DialogTitle>{t("teams.members.add")}</DialogTitle>
              <DialogDescription>
                {t("teams.members.addDescription", {
                  name: selectedTeam.name,
                })}
              </DialogDescription>
            </DialogHeader>
            <TeamMemberPicker
              team={selectedTeam}
              onSaved={() => {
                void invalidate(resourceKeys.teams())
                setParameters({ addMembers: null })
                void invalidate(resourceKeys.teamMembers())
                for (const key of teamMembershipCacheKeys) void invalidate(key)
              }}
              onCancel={() => setParameters({ addMembers: null })}
            />
          </DialogContent>
        </Dialog>
      ) : null}

      <UnsavedDialog
        open={editingTeam && Boolean(selectedTeam)}
        onOpenChange={(open) => !open && setEditingTeam(false)}
      >
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("teams.edit")}</DialogTitle>
            <DialogDescription>{t("teams.editDescription")}</DialogDescription>
          </DialogHeader>
          {editingTeam && selectedTeam ? (
            <TeamForm
              team={selectedTeam}
              onSaved={() => {
                setEditingTeam(false)
                void invalidate(resourceKeys.team(teamId))
              }}
              onCancel={() => setEditingTeam(false)}
            />
          ) : null}
        </DialogContent>
      </UnsavedDialog>

      <ConfirmationDialog
        {...teamDeletion.dialog}
        title={t("teams.delete.title", { name: teamDeletion.item?.name ?? "" })}
        description={t("teams.delete.description", {
          count: teamDeletion.item?.memberCount ?? 0,
        })}
        pendingLabel={tCommon("actions.deleting")}
      />

      <ConfirmationDialog
        {...memberRemoval.dialog}
        title={
          removingTeamMembers.length === 1
            ? t("teams.members.removeTitle", {
                name: removingTeamMembers[0].displayName,
              })
            : t("teams.members.removeMultipleTitle", {
                count: removingTeamMembers.length,
              })
        }
        description={t(
          removingTeamMembers.length === 1
            ? "teams.members.removeDescription"
            : "teams.members.removeMultipleDescription",
        )}
      />
    </>
  )
}
