/** 个人设置中的电脑列表。 */
import { LaptopIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceContent } from "@/components/resource-content"
import { ResourceListFrame } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { useDateTime } from "@/hooks/use-date-time"
import { useComputerPresence, useComputers } from "@/features/settings/use-computers"

/** 展示当前成员已注册的电脑及其在线状态，并可撤销其中一台。 */
export function ComputerListPage() {
  const { t } = useTranslation(["settings", "common"])
  const { formatDateTime } = useDateTime()
  const { resource, computers, localComputerId, revocation } = useComputers()
  const presence = useComputerPresence()

  return (
    <>
      <ResourceContent
        resources={resource}
        errorMessage={t("computers.list.loadError")}
      >
        <ResourceListFrame>
          <ResourceTable
            columns={[
              {
                key: "computer",
                header: t("computers.list.columns.name"),
                cellClassName: "min-w-0",
                cell: (computer) => (
                  <ResourceRowIdentity
                    icon={LaptopIcon}
                    name={computer.name}
                    secondary={computer.platform ? t(`computers.platforms.${computer.platform}`) : undefined}
                    badge={
                      computer.id === localComputerId ? (
                        <StatusBadge variant="muted">{t("computers.list.current")}</StatusBadge>
                      ) : null
                    }
                    description={presence(computer, formatDateTime)}
                  />
                ),
              },
            ]}
            rows={computers}
            rowKey={(computer) => computer.id}
            empty={t("computers.list.empty")}
            rowActions={(computer) => [
              {
                key: "revoke",
                label: t("computers.revoke.action"),
                destructive: true,
                separatorBefore: true,
                onSelect: () => revocation.select(computer),
              },
            ]}
          />
        </ResourceListFrame>
      </ResourceContent>

      <ConfirmationDialog
        {...revocation.dialog}
        title={t("computers.revoke.title", { name: revocation.item?.name ?? "" })}
        description={t("computers.revoke.description")}
        pendingLabel={t("computers.revoke.pending")}
      />
    </>
  )
}
