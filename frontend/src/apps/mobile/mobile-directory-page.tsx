/** 移动端同事与服务台目录的搜索、分页和返回恢复。 */
import { ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate } from "react-router"

import { listColleagues, OrganizationIdentityType } from "@/api"
import type { MobileAgentLocationState } from "@/apps/mobile/mobile-agent-chat-page"
import { useMobileNavigation } from "@/apps/mobile/mobile-navigation"
import { MobilePageHeader, MobileSearchBar } from "@/apps/mobile/mobile-page"
import { MobilePagedList } from "@/apps/mobile/mobile-paged-list"
import { ProfileAvatar } from "@/components/profile-avatar"
import { StatusBadge } from "@/components/status-badge"
import { resourceKeys } from "@/hooks/resource-keys"
import { useListSearchParams } from "@/hooks/use-list-search-params"

/** 防抖同步搜索条件，加载服务台和在职同事。 */
export function MobileDirectoryPage() {
  const { t } = useTranslation(["mobile", "contacts"])
  const { listPageCounts, scrollPositions } = useMobileNavigation()
  // 检索词变化时重置目标查询的加载进度和滚动位置。
  const { query: queryText, search, setSearch } = useListSearchParams({
    onQueryChange: (query) => {
      const storageKey = `employees:${query}`
      listPageCounts.delete(storageKey)
      scrollPositions.delete(storageKey)
    },
  })

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("contacts:scopes.employees")} backTo="/contacts" />
      <MobileSearchBar
        label={t("contacts.search")}
        value={search}
        onChange={setSearch}
      />
      <MobileDirectoryList
        key={`employees:${queryText.trim()}`}
        queryText={queryText.trim()}
        searching={search !== queryText}
      />
    </section>
  )
}

/** 逐页读取服务台和同事，行内展示头像、名称、所属团队，同事带邮箱，服务台带负责人；点击同事查看资料，点击服务台进入新对话。 */
function MobileDirectoryList({
  queryText,
  searching,
}: {
  queryText: string
  searching: boolean
}) {
  const { t } = useTranslation(["mobile", "contacts"])
  const navigate = useNavigate()
  return (
    <MobilePagedList
      storageKey={`employees:${queryText}`}
      searching={searching}
      labels={{
        loadError: t("contacts.loadError"),
        empty: t("contacts.empty"),
        allLoaded: t("contacts.allLoaded"),
      }}
      source={(page) => {
        const query = { query: queryText, page, pageSize: 50 }
        return {
          key: resourceKeys.colleagues(query),
          load: (signal) => listColleagues(query, signal),
        }
      }}
      select={(data) => ({ items: data.colleagues, page: data.page })}
    >
      {(colleagues) => (
        <ul className="divide-y border-b">
          {colleagues.map((colleague) => {
            const serviceDesk =
              colleague.identityType === OrganizationIdentityType.OrganizationIdentityTypeAgent
            const rowClassName =
              "flex min-h-18 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            const content = (
              <>
                <ProfileAvatar
                  name={colleague.displayName}
                  imageURL={colleague.avatarUrl}
                  fallback={serviceDesk ? "agent" : "person"}
                />
                <span className="min-w-0 flex-1">
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="min-w-0 truncate text-[15px] font-medium">
                      {colleague.displayName}
                      {colleague.teams.length ? (
                        <span className="font-normal text-muted-foreground">
                          {" · "}
                          {colleague.teams
                            .map((team) => team.name)
                            .join(t("contacts:teamSelect.separator"))}
                        </span>
                      ) : null}
                    </span>
                    {serviceDesk ? (
                      <StatusBadge variant="muted">{t("contacts:list.serviceDesk")}</StatusBadge>
                    ) : null}
                  </span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {serviceDesk
                      ? colleague.responsibleName &&
                        t("contacts:list.responsible", { name: colleague.responsibleName })
                      : colleague.email}
                  </span>
                </span>
                <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
              </>
            )
            return (
              <li key={colleague.identityId}>
                {serviceDesk ? (
                  <button
                    type="button"
                    className={`w-full text-left ${rowClassName}`}
                    onClick={() =>
                      void navigate(`/chats/agent/${crypto.randomUUID()}`, {
                        state: {
                          draftTarget: {
                            identityId: colleague.identityId,
                            displayName: colleague.displayName,
                          },
                          mobileBack: true,
                        } satisfies MobileAgentLocationState,
                      })
                    }
                  >
                    {content}
                  </button>
                ) : (
                  <Link
                    to={`/contacts/employees/${colleague.userId}`}
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
