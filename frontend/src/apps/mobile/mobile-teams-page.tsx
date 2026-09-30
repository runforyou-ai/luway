/** 移动端团队列表与团队成员的只读浏览和工作状态筛选。 */
import { useState } from "react"
import { ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useLocation, useNavigate, useParams } from "react-router"

import type { MobileAgentLocationState } from "@/apps/mobile/mobile-agent-chat-page"
import {
  getTeam,
  listTeamMembers,
  listTeams,
  OrganizationIdentityType,
  WorkStatus,
} from "@/api"
import { MobileFilterSheet } from "@/apps/mobile/mobile-filter-sheet"
import { useMobileNavigation } from "@/apps/mobile/mobile-navigation"
import { MobilePageHeader, MobileSearchBar } from "@/apps/mobile/mobile-page"
import { MobilePagedList } from "@/apps/mobile/mobile-paged-list"
import { ProfileAvatar } from "@/components/profile-avatar"
import { Button } from "@/components/ui/button"
import { WorkStatusBadge, workStatusLabel } from "@/components/work-status"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useResource } from "@/hooks/use-resource"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 团队成员工作状态筛选的可选项，空值表示全部状态。 */
const workStatusFilters = [
  "",
  WorkStatus.WorkStatusWorking,
  WorkStatus.WorkStatusAway,
  WorkStatus.WorkStatusOffDuty,
] as const

/** 防抖同步团队名称搜索，展示企业团队及其成员人数，点击进入成员名单。 */
export function MobileTeamsPage() {
  const { t } = useTranslation(["mobile", "contacts"])
  const { listPageCounts, scrollPositions } = useMobileNavigation()
  // 检索词变化时重置目标查询的加载进度和滚动位置。
  const { query: queryText, search, setSearch } = useListSearchParams({
    onQueryChange: (query) => {
      const storageKey = `teams:${query}`
      listPageCounts.delete(storageKey)
      scrollPositions.delete(storageKey)
    },
  })
  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("contacts:scopes.teams")} backTo="/contacts" />
      <MobileSearchBar
        label={t("contacts:search.teams")}
        value={search}
        onChange={setSearch}
      />
      <MobileTeamList
        key={`teams:${queryText.trim()}`}
        queryText={queryText.trim()}
        searching={search !== queryText}
      />
    </section>
  )
}

/** 逐页读取团队，行内展示名称和描述或成员人数。 */
function MobileTeamList({
  queryText,
  searching,
}: {
  queryText: string
  searching: boolean
}) {
  const { t } = useTranslation(["mobile", "contacts"])
  return (
    <MobilePagedList
      storageKey={`teams:${queryText}`}
      searching={searching}
      labels={{
        loadError: t("teams.loadError"),
        empty: queryText ? t("contacts:teams.emptyFiltered") : t("teams.empty"),
        allLoaded: t("teams.allLoaded"),
      }}
      source={(page) => {
        const query = { query: queryText, page, pageSize: 50 }
        return {
          key: resourceKeys.teams(query),
          load: (signal) => listTeams(query, signal),
        }
      }}
      select={(data) => ({ items: data.teams, page: data.page })}
    >
      {(teams) => (
        <ul className="divide-y border-b">
          {teams.map((team) => (
            <li key={team.id}>
              <Link
                to={`/contacts/teams/${team.id}`}
                state={{ mobileBack: true }}
                className="flex min-h-18 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[15px] font-medium">
                    {team.name}
                  </span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {team.description ||
                      t("teams.memberCount", { count: team.memberCount })}
                  </span>
                </span>
                <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </MobilePagedList>
  )
}

/** 防抖同步姓名搜索，按工作状态筛选并展示团队内的真人成员和 AI 员工。 */
export function MobileTeamMembersPage() {
  const { t } = useTranslation(["mobile", "contacts"])
  const { t: tCommon } = useTranslation("common")
  const { teamID = "" } = useParams()
  const location = useLocation()
  const { listPageCounts, scrollPositions } = useMobileNavigation()
  // 检索词变化时重置目标查询的加载进度和滚动位置。
  const {
    searchParams,
    setParameters,
    query: queryText,
    search,
    setSearch,
  } = useListSearchParams({
    onQueryChange: (query) => {
      const storageKey = `team:${teamID}:${workStatus ?? ""}:${query}`
      listPageCounts.delete(storageKey)
      scrollPositions.delete(storageKey)
    },
  })
  const workStatus = optionalWailsEnum(WorkStatus, searchParams.get("workStatus"))
  const [draftWorkStatus, setDraftWorkStatus] = useState<WorkStatus | "">("")
  // 筛选项文案，空值为全部状态。
  const filterLabel = (status: WorkStatus | "") =>
    status
      ? workStatusLabel(status, tCommon)
      : t("contacts:filters.allWorkStatuses")
  // 读不到团队名称时回到通用标题。
  const { data: team } = useResource(resourceKeys.team(teamID), () => getTeam(teamID))

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={team?.name ?? t("contacts:scopes.teams")}
        backTo="/contacts/teams"
      />
      <MobileSearchBar
        label={t("contacts:search.teamMembers")}
        value={search}
        onChange={setSearch}
      />
      <MobileFilterSheet
        summary={workStatus ? filterLabel(workStatus) : ""}
        onOpen={() => setDraftWorkStatus(workStatus ?? "")}
        onReset={() => setDraftWorkStatus("")}
        onApply={() => {
          // 切换工作状态时重置目标查询的加载进度和滚动位置。
          const storageKey = `team:${teamID}:${draftWorkStatus}:${queryText.trim()}`
          listPageCounts.delete(storageKey)
          scrollPositions.delete(storageKey)
          setParameters(
            { workStatus: draftWorkStatus || null },
            true,
            location.state,
          )
        }}
      >
        <div
          role="group"
          aria-label={t("contacts:filters.workStatus")}
          className="grid grid-cols-2 gap-2"
        >
          {workStatusFilters.map((status) => (
            <Button
              key={status}
              variant={draftWorkStatus === status ? "default" : "outline"}
              className="min-h-11"
              aria-pressed={draftWorkStatus === status}
              onClick={() => setDraftWorkStatus(status)}
            >
              {filterLabel(status)}
            </Button>
          ))}
        </div>
      </MobileFilterSheet>
      <MobileTeamMemberList
        key={`team:${teamID}:${workStatus ?? ""}:${queryText.trim()}`}
        teamID={teamID}
        workStatus={workStatus}
        queryText={queryText.trim()}
        searching={search !== queryText}
      />
    </section>
  )
}

/** 逐页读取团队成员，真人进成员资料，AI 员工进对话。 */
function MobileTeamMemberList({
  teamID,
  workStatus,
  queryText,
  searching,
}: {
  teamID: string
  workStatus: WorkStatus | undefined
  queryText: string
  searching: boolean
}) {
  const { t } = useTranslation("mobile")
  const navigate = useNavigate()
  const { formatDateTime } = useDateTime()
  const rowClassName =
    "flex min-h-18 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
  return (
    <MobilePagedList
      storageKey={`team:${teamID}:${workStatus ?? ""}:${queryText}`}
      searching={searching}
      labels={{
        loadError: t("teams.membersLoadError"),
        empty: t("teams.membersEmpty"),
        allLoaded: t("contacts.allLoaded"),
      }}
      source={(page) => {
        const query = { query: queryText, workStatus, page, pageSize: 50 }
        return {
          key: resourceKeys.teamMembers(teamID, query),
          load: (signal) => listTeamMembers(teamID, query, signal),
        }
      }}
      select={(data) => ({ items: data.members, page: data.page })}
    >
      {(members) => (
        <ul className="divide-y border-b">
          {members.map((member) => {
            const agent =
              member.identityType === OrganizationIdentityType.OrganizationIdentityTypeAgent
            const content = (
              <>
                <ProfileAvatar
                  name={member.displayName}
                  imageURL={member.avatarUrl}
                  fallback={agent ? "agent" : "person"}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[15px] font-medium">
                    {member.displayName}
                  </span>
                  <span className="mt-0.5 flex items-center gap-2">
                    <WorkStatusBadge status={member.workStatus} />
                    <span className="truncate text-xs text-muted-foreground">
                      {t("teams.joinedAt", {
                        time: formatDateTime(member.joinedAt),
                      })}
                    </span>
                  </span>
                </span>
                <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
              </>
            )
            return (
              <li key={member.identityId}>
                {agent ? (
                  <button
                    type="button"
                    className={`w-full text-left ${rowClassName}`}
                    onClick={() =>
                      void navigate(`/chats/agent/${crypto.randomUUID()}`, {
                        state: {
                          draftAgentID: member.agentId,
                          mobileBack: true,
                        } satisfies MobileAgentLocationState,
                      })
                    }
                  >
                    {content}
                  </button>
                ) : (
                  <Link
                    to={`/contacts/employees/${member.userId}`}
                    state={{ mobileBack: true }}
                    className={rowClassName}
                  >
                    {content}
                  </Link>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </MobilePagedList>
  )
}
