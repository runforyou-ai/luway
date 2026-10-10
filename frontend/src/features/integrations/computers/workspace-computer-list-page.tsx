/** 工作区电脑列表页。 */
import { useState } from "react"
import { CircleHelpIcon, PlusIcon, ServerIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  listWorkspaceComputers,
  resetComputerCredential,
  revokeComputer,
  type Computer,
  type ComputerRegistration,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { PageHeader } from "@/components/page-header"
import { ProductDocSheet } from "@/components/product-doc-sheet"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import { WorkspaceComputerDialog } from "@/features/integrations/computers/workspace-computer-dialog"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useDateTime } from "@/hooks/use-date-time"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"

/** 显示当前工作区的工作区电脑，可添加电脑、重置凭据与删除。 */
export function WorkspaceComputerListPage() {
  const { t } = useTranslation(["integrations", "settings", "common"])
  const { formatDateTime } = useDateTime()
  const invalidate = useResourceInvalidator()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [registration, setRegistration] = useState<ComputerRegistration | null>(null)
  const [docsTrigger, setDocsTrigger] = useState<HTMLElement | null>(null)
  // 定时刷新，执行器连接或断开后列表跟上在线状态。
  const resource = useResource(resourceKeys.workspaceComputers(), () => listWorkspaceComputers(), {
    staleTime: 0,
    refetchInterval: 15_000,
    refetchOnWindowFocus: true,
  })
  const computers = resource.data?.computers ?? []

  /** 在弹窗中展示新得到的连接信息并刷新列表。 */
  function showRegistration(next: ComputerRegistration) {
    setRegistration(next)
    setDialogOpen(true)
    void invalidate(resourceKeys.workspaceComputers())
  }

  const reset = useConfirmedAction<Computer, ComputerRegistration>({
    action: (computer) => resetComputerCredential(computer.id),
    onSuccess: (_computer, next) => showRegistration(next),
    logLabel: "重置工作区电脑凭据",
    errorMessage: () => t("computer.reset.error"),
  })
  const deletion = useConfirmedAction<Computer>({
    action: (computer) => revokeComputer(computer.id),
    invalidateKeys: () => [resourceKeys.workspaceComputers(), resourceKeys.agent()],
    logLabel: "删除工作区电脑",
    successMessage: () => t("computer.delete.success"),
    errorMessage: () => t("computer.delete.error"),
  })

  /** 返回电脑的平台与在线说明。 */
  function presence(computer: Computer) {
    if (computer.online) return t("settings:computers.list.online")
    if (computer.lastSeenAt) return t("settings:computers.list.lastSeen", { time: formatDateTime(computer.lastSeenAt) })
    return t("settings:computers.list.neverConnected")
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("computer.title")} description={t("computer.description")}>
        <Button type="button" variant="ghost" size="sm" onClick={(event) => setDocsTrigger(event.currentTarget)}>
          <CircleHelpIcon />
          {t("common:productDocs")}
        </Button>
        <Button
          type="button"
          variant="subtle"
          size="icon-sm"
          aria-label={t("computer.create.action")}
          title={t("computer.create.action")}
          onClick={() => {
            setRegistration(null)
            setDialogOpen(true)
          }}
        >
          <PlusIcon />
        </Button>
      </PageHeader>
      <ResourceListLayout resources={resource} errorMessage={t("computer.list.loadError")}>
        <ResourceTable
          columns={[
            {
              key: "computer",
              header: t("computer.list.columns.name"),
              cellClassName: "min-w-0",
              cell: (computer) => (
                <ResourceRowIdentity
                  icon={ServerIcon}
                  name={computer.name}
                  secondary={computer.platform ? t(`settings:computers.platforms.${computer.platform}`) : undefined}
                  description={[presence(computer), t("computer.list.agents", { count: computer.agentCount })].join(" · ")}
                />
              ),
            },
            {
              key: "time",
              header: t("common:time.addedAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (computer) => t("common:time.addedAt", { time: formatDateTime(computer.createdAt) }),
            },
          ]}
          rows={computers}
          rowKey={(computer) => computer.id}
          empty={t("computer.list.empty")}
          rowActions={(computer) => [
            {
              key: "reset",
              label: t("computer.reset.action"),
              onSelect: () => reset.select(computer),
            },
            {
              key: "delete",
              label: t("common:actions.delete"),
              destructive: true,
              separatorBefore: true,
              onSelect: () => deletion.select(computer),
            },
          ]}
        />
      </ResourceListLayout>

      <WorkspaceComputerDialog
        open={dialogOpen}
        registration={registration}
        onOpenChange={setDialogOpen}
        onCreated={showRegistration}
      />
      <ConfirmationDialog
        {...reset.dialog}
        destructive
        title={reset.item ? t("computer.reset.title", { name: reset.item.name }) : ""}
        description={t("computer.reset.description")}
        pendingLabel={t("computer.reset.pending")}
      />
      <ConfirmationDialog
        {...deletion.dialog}
        title={deletion.item ? t("computer.delete.title", { name: deletion.item.name }) : ""}
        description={t("computer.delete.description")}
        pendingLabel={t("common:actions.deleting")}
      />
      <ProductDocSheet page={docsTrigger ? "computers" : null} trigger={docsTrigger} onClose={() => setDocsTrigger(null)} />
    </div>
  )
}
