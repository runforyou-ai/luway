/** MCP 服务列表页。 */
import { useState } from "react"
import { PlugIcon, PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate } from "react-router"
import { toast } from "sonner"

import {
  deleteMCPServer,
  isApiError,
  listMCPServers,
  refreshMCPServerTools,
  type MCPServerData,
} from "@/api"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { PageHeader } from "@/components/page-header"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { Button } from "@/components/ui/button"
import { MCPServerToolsCell } from "@/features/integrations/mcp-servers/mcp-server-tools-cell"
import { useMCPServerConnectionTest } from "@/features/integrations/mcp-servers/use-mcp-server-connection-test"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 显示当前企业配置的 MCP 服务。 */
export function MCPServerListPage() {
  const { t } = useTranslation(["integrations", "common"])
  const navigate = useNavigate()
  const [submittingRefresh, setSubmittingRefresh] = useState(false)
  const connectionTest = useMCPServerConnectionTest()
  const mounted = useMountedRef()
  const resource = useResource(resourceKeys.mcpServers(), () => listMCPServers(), {
    staleTime: 0,
    refetchInterval: (data) => data?.mcpServers.some((server) => server.toolsUpdating) ? 1000 : false,
    refetchOnWindowFocus: true,
  })
  const { data } = resource
  const mcpServers = data?.mcpServers ?? []

  /** 提交全部服务的更新任务，并读取服务端返回的更新状态。 */
  async function updateTools() {
    if (submittingRefresh) return
    setSubmittingRefresh(true)
    try {
      await refreshMCPServerTools()
      await resource.refresh()
    } catch (error) {
      if (!mounted.current || recoverSession(error, navigate)) return
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("mcpServer.tools.submitError"))
    } finally {
      if (mounted.current) setSubmittingRefresh(false)
    }
  }

  const deletion = useConfirmedAction<MCPServerData>({
    action: (server) => deleteMCPServer(server.id),
    invalidateKeys: (server) => [
      resourceKeys.mcpServers(),
      resourceKeys.mcpServer(server.id),
      resourceKeys.agentMCPServerOptions(),
      resourceKeys.agent(),
      resourceKeys.personalAgent(),
    ],
    logLabel: "MCP 服务删除",
    successMessage: () => t("mcpServer.delete.success"),
    errorMessage: () => t("mcpServer.delete.error"),
  })

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={t("mcpServer.title")}
        description={t("mcpServer.description")}
      >
        <Button
          size="sm"
          variant="outline"
          disabled={submittingRefresh || !mcpServers.length || mcpServers.some((server) => server.toolsUpdating)}
          onClick={() => void updateTools()}
        >
          {t("mcpServer.tools.refresh")}
        </Button>
        <Button variant="subtle" size="icon-sm" asChild>
          <Link
            to="/tools/new"
            aria-label={t("mcpServer.list.create")}
            title={t("mcpServer.list.create")}
          >
            <PlusIcon />
          </Link>
        </Button>
      </PageHeader>
      <ResourceListLayout
        resources={resource}
        errorMessage={t("mcpServer.list.loadError")}
      >
        <ResourceTable
          columns={[
            {
              key: "server",
              header: t("mcpServer.list.columns.name"),
              cellClassName: "min-w-0",
              cell: (mcpServer) => (
                <ResourceRowIdentity
                  icon={PlugIcon}
                  name={mcpServer.name}
                  description={<MCPServerToolsCell server={mcpServer} />}
                />
              ),
            },
          ]}
          rows={mcpServers}
          rowKey={(mcpServer) => mcpServer.id}
          empty={t("mcpServer.list.empty")}
          onRowActivate={(mcpServer) =>
            navigate(`/tools/${mcpServer.id}`)
          }
          rowActions={(mcpServer) => {
            const testing = connectionTest.testingIds.has(mcpServer.id)
            return [
              {
                key: "test",
                label: testing
                  ? t("connection.testing")
                  : t("connection.test"),
                disabled: testing,
                onSelect: () => void connectionTest.test(mcpServer.id),
              },
              {
                key: "delete",
                label: t("common:actions.delete"),
                destructive: true,
                separatorBefore: true,
                onSelect: () => deletion.select(mcpServer),
              },
            ]
          }}
        />
      </ResourceListLayout>

      <ConfirmationDialog
        {...deletion.dialog}
        title={
          deletion.item ? t("mcpServer.delete.title", { name: deletion.item.name }) : ""
        }
        description={t("mcpServer.delete.description")}
        pendingLabel={t("common:actions.deleting")}
      />
    </div>
  )
}
