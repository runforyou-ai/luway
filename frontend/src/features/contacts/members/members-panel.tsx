/** 同事目录面板：搜索在职同事和服务台并发起聊天。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { OrganizationIdentityType, listColleagues, type ColleagueData } from "@/api"
import { ListToolbarSearch, ListToolbarTotal } from "@/components/list-toolbar"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { WorkStatusDot } from "@/components/work-status"
import { useWorkspace } from "@/contexts/workspace-context"
import { ContactListSection } from "@/features/contacts/contact-list-section"
import { useContactSearch } from "@/hooks/use-contact-search"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { usePagedResource } from "@/hooks/use-resource"

/** 服务台在前、在职同事在后的目录，点击任一行进入与其单聊。 */
export function MembersPanel() {
  const { t } = useTranslation(["contacts", "common"])
  const { formatDateTime } = useDateTime()
  const { identity } = useWorkspace()
  const navigate = useNavigate()
  const { query, search, setSearch } = useContactSearch()

  const list = usePagedResource(
    resourceKeys.colleagues({ query, pageSize: 50 }),
    (page) => listColleagues({ query, page, pageSize: 50 }),
    {
      select: (data) => ({ items: data.colleagues, page: data.page }),
      itemKey: (colleague) => colleague.identityId,
    },
  )
  const colleagues = list.data?.items ?? []

  return (
    <ContactListSection
        title={t("scopes.employees")}
        description={t("scopeDescriptions.employees")}
        scope="employees"
        toolbar={
          <>
            <ListToolbarSearch
              value={search}
              aria-label={t("search.employees")}
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
              key: "employee",
              header: t("columns.employeeName"),
              cellClassName: "min-w-0",
              cell: (colleague: ColleagueData) => {
                const serviceDesk =
                  colleague.identityType === OrganizationIdentityType.OrganizationIdentityTypeAgent
                return (
                  <ResourceRowIdentity
                    avatar={{
                      imageURL: colleague.avatarUrl,
                      name: colleague.displayName,
                      fallback: serviceDesk ? "agent" : "person",
                    }}
                    mark={
                      <WorkStatusDot
                        status={
                          colleague.identityId === identity.user.identityId
                            ? identity.user.workStatus
                            : colleague.workStatus
                        }
                      />
                    }
                    name={colleague.displayName}
                    secondary={colleague.teams.map((team) => team.name).join(t("teamSelect.separator"))}
                    badge={
                      serviceDesk ? (
                        <StatusBadge variant="muted">{t("list.serviceDesk")}</StatusBadge>
                      ) : undefined
                    }
                    description={
                      serviceDesk
                        ? colleague.responsibleName &&
                          t("list.responsible", { name: colleague.responsibleName })
                        : colleague.email
                    }
                  />
                )
              },
            },
            {
              key: "time",
              header: t("common:time.addedAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (colleague) =>
                t("common:time.addedAt", { time: formatDateTime(colleague.createdAt) }),
            },
          ]}
          rows={colleagues}
          rowKey={(colleague) => colleague.identityId}
          empty={t("list.empty")}
          onRowActivate={(colleague) => navigate(`/chats?target=${colleague.identityId}`)}
          // 自己的行不可进入。
          canActivateRow={(colleague) => colleague.identityId !== identity.user.identityId}
        />
    </ContactListSection>
  )
}
