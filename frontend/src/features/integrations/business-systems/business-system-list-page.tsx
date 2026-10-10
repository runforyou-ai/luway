/** 业务系统列表页。 */
import { useMutation } from "@tanstack/react-query"
import { PlugIcon, PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate } from "react-router"

import {
  deleteBusinessSystem,
  listBusinessSystems,
  refreshBusinessSystemTools,
  type BusinessSystem,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import { BusinessSystemToolsCell } from "@/features/integrations/business-systems/business-system-tools-cell"
import { useBusinessSystemConnectionTest } from "@/features/integrations/business-systems/use-business-system-connection-test"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource } from "@/hooks/use-resource"

/** 显示当前工作区配置的业务系统。 */
export function BusinessSystemListPage() {
  const { t } = useTranslation(["integrations", "common"])
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()
  const connectionTest = useBusinessSystemConnectionTest()
  const resource = useResource(resourceKeys.businessSystems(), () => listBusinessSystems(), {
    staleTime: 0,
    refetchInterval: (data) => data?.businessSystems.some((system) => system.toolsUpdating) ? 1000 : false,
    refetchOnWindowFocus: true,
  })
  const { data } = resource
  const businessSystems = data?.businessSystems ?? []

  const toolsRefresh = useMutation({
    mutationFn: async () => {
      await refreshBusinessSystemTools()
      await resource.refresh()
    },
  })
  const submittingRefresh = toolsRefresh.isPending

  /** 提交全部业务系统的工具目录更新任务，并读取服务端返回的更新状态。 */
  function updateTools() {
    toolsRefresh.mutate(undefined, {
      onError: (error) => reportError(error, { fallback: t("businessSystem.tools.submitError") }),
    })
  }

  const deletion = useConfirmedAction<BusinessSystem>({
    action: (system) => deleteBusinessSystem(system.id),
    invalidateKeys: (system) => [
      resourceKeys.businessSystems(),
      resourceKeys.businessSystem(system.id),
      resourceKeys.agentBusinessSystemOptions(),
      resourceKeys.agent(),
      resourceKeys.personalAgent(),
    ],
    logLabel: "业务系统删除",
    successMessage: () => t("businessSystem.delete.success"),
    errorMessage: () => t("businessSystem.delete.error"),
  })

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={t("businessSystem.title")}
        description={t("businessSystem.description")}
      >
        <Button
          size="sm"
          variant="outline"
          disabled={submittingRefresh || !businessSystems.length || businessSystems.some((system) => system.toolsUpdating)}
          onClick={updateTools}
        >
          {t("businessSystem.tools.refresh")}
        </Button>
        <Button variant="subtle" size="icon-sm" asChild>
          <Link
            to="/business-systems/new"
            aria-label={t("businessSystem.list.create")}
            title={t("businessSystem.list.create")}
          >
            <PlusIcon />
          </Link>
        </Button>
      </PageHeader>
      <ResourceListLayout
        resources={resource}
        errorMessage={t("businessSystem.list.loadError")}
      >
        <ResourceTable
          columns={[
            {
              key: "system",
              header: t("businessSystem.list.columns.name"),
              cellClassName: "min-w-0",
              cell: (system) => (
                <ResourceRowIdentity
                  icon={PlugIcon}
                  name={system.name}
                  description={<BusinessSystemToolsCell system={system} />}
                />
              ),
            },
          ]}
          rows={businessSystems}
          rowKey={(system) => system.id}
          empty={t("businessSystem.list.empty")}
          onRowActivate={(system) =>
            navigate(`/business-systems/${system.id}`)
          }
          rowActions={(system) => {
            const testing = connectionTest.testingIds.has(system.id)
            return [
              {
                key: "test",
                label: testing
                  ? t("connection.testing")
                  : t("connection.test"),
                disabled: testing,
                onSelect: () => void connectionTest.test(system.id),
              },
              {
                key: "delete",
                label: t("common:actions.delete"),
                destructive: true,
                separatorBefore: true,
                onSelect: () => deletion.select(system),
              },
            ]
          }}
        />
      </ResourceListLayout>

      <ConfirmationDialog
        {...deletion.dialog}
        title={
          deletion.item ? t("businessSystem.delete.title", { name: deletion.item.name }) : ""
        }
        description={t("businessSystem.delete.description")}
        pendingLabel={t("common:actions.deleting")}
      />
    </div>
  )
}
