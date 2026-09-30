/** 个人设置中的设备列表。 */
import { LaptopIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceContent } from "@/components/resource-content"
import { ResourceListFrame } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { useDateTime } from "@/hooks/use-date-time"
import { useDevices } from "@/features/settings/use-devices"

/** 展示当前用户已注册的设备，并可撤销其中一台。 */
export function DeviceListPage() {
  const { t } = useTranslation(["settings", "common"])
  const { formatDateTime } = useDateTime()
  const { resource, devices, localDeviceId, revocation } = useDevices()

  return (
    <>
      <ResourceContent
        resources={resource}
        errorMessage={t("devices.list.loadError")}
      >
        <ResourceListFrame>
          <ResourceTable
            columns={[
              {
                key: "device",
                header: t("devices.list.columns.name"),
                cellClassName: "min-w-0",
                cell: (device) => (
                  <ResourceRowIdentity
                    icon={LaptopIcon}
                    name={device.name}
                    secondary={t(`devices.platforms.${device.platform}`)}
                    badge={
                      device.id === localDeviceId ? (
                        <StatusBadge variant="muted">{t("devices.list.current")}</StatusBadge>
                      ) : null
                    }
                    description={t("devices.list.registeredAt", {
                      time: formatDateTime(device.createdAt),
                    })}
                  />
                ),
              },
            ]}
            rows={devices}
            rowKey={(device) => device.id}
            empty={t("devices.list.empty")}
            rowActions={(device) => [
              {
                key: "revoke",
                label: t("devices.revoke.action"),
                destructive: true,
                separatorBefore: true,
                onSelect: () => revocation.select(device),
              },
            ]}
          />
        </ResourceListFrame>
      </ResourceContent>

      <ConfirmationDialog
        {...revocation.dialog}
        title={t("devices.revoke.title", { name: revocation.item?.name ?? "" })}
        description={t("devices.revoke.description")}
        pendingLabel={t("devices.revoke.pending")}
      />
    </>
  )
}
