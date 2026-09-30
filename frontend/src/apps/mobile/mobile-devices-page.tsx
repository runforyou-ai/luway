/** 移动端个人中心的设备列表与撤销设备。 */
import { useRef } from "react"
import { LaptopIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  MobilePageHeader,
  MobilePageState,
  MobileScrollArea,
} from "@/apps/mobile/mobile-page"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { LoadingIndicator } from "@/components/loading-indicator"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { useDevices } from "@/features/settings/use-devices"
import { useDateTime } from "@/hooks/use-date-time"

/** 展示当前用户已注册的设备，行尾撤销按钮经二次确认后撤销。 */
export function MobileDevicesPage() {
  const { t } = useTranslation(["mobile", "settings", "common"])
  const { formatDateTime } = useDateTime()
  const { resource, devices, localDeviceId, revocation } = useDevices()
  const trigger = useRef<HTMLElement | null>(null)

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("me.devices")} backTo="/me" />
      <MobileScrollArea storageKey="me-devices" ready={Boolean(resource.data)}>
        {resource.data ? (
          devices.length ? (
            <ul className="divide-y border-b">
              {devices.map((device) => (
                <li key={device.id} className="flex min-h-18 items-center gap-3 px-4 py-3">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
                    <LaptopIcon className="size-5" aria-hidden="true" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span
                      id={`mobile-device-${device.id}`}
                      className="flex min-w-0 items-center gap-2 text-[15px] font-medium"
                    >
                      <span className="min-w-0 truncate">
                        {device.name}
                        <span className="font-normal text-muted-foreground">
                          {" · "}
                          {t(`settings:devices.platforms.${device.platform}`)}
                        </span>
                      </span>
                      {device.id === localDeviceId ? (
                        <StatusBadge variant="muted">{t("settings:devices.list.current")}</StatusBadge>
                      ) : null}
                    </span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {t("settings:devices.list.registeredAt", {
                        time: formatDateTime(device.createdAt),
                      })}
                    </span>
                  </span>
                  <Button
                    type="button"
                    variant="outline"
                    className="min-h-11 shrink-0"
                    disabled={revocation.pending}
                    aria-describedby={`mobile-device-${device.id}`}
                    onClick={(event) => {
                      trigger.current = event.currentTarget
                      revocation.select(device)
                    }}
                  >
                    {t("settings:devices.revoke.action")}
                  </Button>
                </li>
              ))}
            </ul>
          ) : (
            <MobilePageState title={t("settings:devices.list.empty")} />
          )
        ) : resource.loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : resource.error ? (
          <MobilePageState
            title={t("settings:devices.list.loadError")}
            onRetry={() => void resource.refresh()}
          />
        ) : null}
      </MobileScrollArea>
      <ConfirmationDialog
        {...revocation.dialog}
        title={t("settings:devices.revoke.title", { name: revocation.item?.name ?? "" })}
        description={t("settings:devices.revoke.description")}
        pendingLabel={t("settings:devices.revoke.pending")}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          trigger.current?.focus({ preventScroll: true })
        }}
      />
    </section>
  )
}
