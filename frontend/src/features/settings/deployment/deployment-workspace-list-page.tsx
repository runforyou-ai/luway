/** 部署设置的全部工作区页：搜索并查看部署内的全部工作区及其成员规模。 */
import { LayoutGridIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { listDeploymentWorkspaces, type DeploymentWorkspace } from "@/api"
import { ListToolbar, ListToolbarSearch, ListToolbarTotal } from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { usePagedResource } from "@/hooks/use-resource"

/** 按创建先后列出部署工作区，只读展示名称、标识、有效成员数和创建时间。 */
export function DeploymentWorkspaceListPage() {
  const { t } = useTranslation("deployment")
  const { formatDateTime } = useDateTime()
  const { query, search, setSearch } = useListSearchParams()
  const list = usePagedResource(
    resourceKeys.deploymentWorkspaces({ query, pageSize: 50 }),
    (page, signal) => listDeploymentWorkspaces({ query, page, pageSize: 50 }, signal),
    { select: (data) => ({ items: data.workspaces, page: data.page }), itemKey: (item) => item.id },
  )

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("workspaces.title")} description={t("workspaces.description")} />

      <ListToolbar>
        <ListToolbarSearch value={search} aria-label={t("workspaces.search")} onChange={(event) => setSearch(event.target.value)} />
        <ListToolbarTotal count={list.data?.total} />
      </ListToolbar>

      <ResourceListLayout resources={list} errorMessage={t("workspaces.loadError")} more={list.more}>
        <ResourceTable<DeploymentWorkspace>
          columns={[
            {
              key: "workspace",
              header: t("workspaces.title"),
              cellClassName: "min-w-0",
              cell: (item) => (
                <ResourceRowIdentity
                  icon={LayoutGridIcon}
                  name={item.name}
                  secondary={t("workspaces.memberCount", { count: item.memberCount })}
                  description={item.slug}
                />
              ),
            },
            {
              key: "time",
              header: t("workspaces.createdAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (item) => t("workspaces.createdAt", { time: formatDateTime(item.createdAt) }),
            },
          ]}
          rows={list.data?.items ?? []}
          rowKey={(item) => item.id}
          empty={t("workspaces.empty")}
        />
      </ResourceListLayout>
    </div>
  )
}
