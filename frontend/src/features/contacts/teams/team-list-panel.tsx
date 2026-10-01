/** 团队列表：新建、进入团队成员页、编辑与删除团队。 */
import { useState } from "react"
import { PlusIcon, UsersIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { deleteTeam, listTeams, type Team } from "@/api"
import { ListToolbarSearch, ListToolbarTotal } from "@/components/list-toolbar"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { UnsavedDialog } from "@/components/unsaved-dialog"
import { Button } from "@/components/ui/button"
import {
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ContactListSection } from "@/features/contacts/contact-list-section"
import { TeamForm } from "@/features/contacts/teams/team-form"
import { teamMembershipCacheKeys } from "@/features/contacts/teams/team-membership-cache"
import { useContactSearch } from "@/hooks/use-contact-search"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useDateTime } from "@/hooks/use-date-time"
import { usePagedResource } from "@/hooks/use-resource"
import { useReturnLink } from "@/hooks/use-return-to"

/** 列出企业团队并提供团队维护入口。 */
export function TeamListPanel() {
  const { t } = useTranslation(["contacts", "common"])
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const returnLink = useReturnLink({ omitSearchParams: ["newTeam"] })
  const { searchParams, setParameters, query, search, setSearch } =
    useContactSearch()
  const [editingTeam, setEditingTeam] = useState<Team | null>(null)
  const creatingTeam = searchParams.get("newTeam") === "1"

  const list = usePagedResource(
    resourceKeys.teams({ query, pageSize: 50 }),
    (page) => listTeams({ query, page, pageSize: 50 }),
    { select: (data) => ({ items: data.teams, page: data.page }), itemKey: (team) => team.id },
  )

  const teamDeletion = useConfirmedAction<Team>({
    action: (team) => deleteTeam(team.id),
    invalidateKeys: () => [resourceKeys.teams(), resourceKeys.serviceCategories(), ...teamMembershipCacheKeys],
    successMessage: () => t("teams.delete.success"),
    errorMessage: () => t("teams.delete.error"),
    logLabel: "删除团队",
  })

  return (
    <>
      <ContactListSection
        title={t("scopes.teams")}
        description={t("scopeDescriptions.teamList")}
        scope="team"
        headerActions={
          <Button
            variant="subtle"
            size="icon-sm"
            aria-label={t("teams.create")}
            title={t("teams.create")}
            onClick={() => setParameters({ newTeam: "1" })}
          >
            <PlusIcon />
          </Button>
        }
        toolbar={
          <>
            <ListToolbarSearch
              value={search}
              aria-label={t("search.teams")}
              onChange={(event) => setSearch(event.target.value)}
            />
            <ListToolbarTotal count={list.data?.total} />
          </>
        }
        list={list}
        more={list.more}
      >
        <ResourceTable
          columns={[
            {
              key: "name",
              header: t("teams.form.name"),
              cellClassName: "min-w-0",
              cell: (team) => (
                <ResourceRowIdentity
                  icon={UsersIcon}
                  name={team.name}
                  secondary={t("teams.memberCount", { count: team.memberCount })}
                  description={team.description || undefined}
                />
              ),
            },
            {
              key: "time",
              header: t("columns.createdAt"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (team) =>
                t("list.createdAt", { time: formatDateTime(team.createdAt) }),
            },
          ]}
          rows={list.data?.items ?? []}
          rowKey={(team) => team.id}
          empty={query ? t("teams.emptyFiltered") : t("teams.empty")}
          onRowActivate={(team) =>
            navigate(
              returnLink(`/contacts/teams/${team.id}`),
            )
          }
          rowActions={(team) => [
            {
              key: "edit",
              label: t("common:actions.edit"),
              onSelect: () => setEditingTeam(team),
            },
            {
              key: "delete",
              label: t("common:actions.delete"),
              destructive: true,
              separatorBefore: true,
              onSelect: () => teamDeletion.select(team),
            },
          ]}
        />
      </ContactListSection>

      <UnsavedDialog
        open={creatingTeam}
        onOpenChange={(open) => !open && setParameters({ newTeam: null })}
      >
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("teams.create")}</DialogTitle>
            <DialogDescription>{t("teams.createDescription")}</DialogDescription>
          </DialogHeader>
          <TeamForm
            onSaved={(team) => {
              navigate(returnLink(`/contacts/teams/${team.id}`), { replace: true })
            }}
            onCancel={() => setParameters({ newTeam: null })}
          />
        </DialogContent>
      </UnsavedDialog>

      <UnsavedDialog
        open={editingTeam !== null}
        onOpenChange={(open) => !open && setEditingTeam(null)}
      >
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("teams.edit")}</DialogTitle>
            <DialogDescription>{t("teams.editDescription")}</DialogDescription>
          </DialogHeader>
          {editingTeam ? (
            <TeamForm
              team={editingTeam}
              onSaved={() => {
                setEditingTeam(null)
              }}
              onCancel={() => setEditingTeam(null)}
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
        pendingLabel={t("common:actions.deleting")}
      />
    </>
  )
}
