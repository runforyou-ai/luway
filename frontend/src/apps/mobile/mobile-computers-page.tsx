/** 移动端个人中心的电脑列表与撤销电脑。 */
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
import { useComputerPresence, useComputers } from "@/features/settings/use-computers"
import { useDateTime } from "@/hooks/use-date-time"

/** 展示当前成员已注册的电脑及其在线状态，行尾撤销按钮经二次确认后撤销。 */
export function MobileComputersPage() {
  const { t } = useTranslation(["mobile", "settings", "common"])
  const { formatDateTime } = useDateTime()
  const { resource, computers, localComputerId, revocation } = useComputers()
  const presence = useComputerPresence()
  const trigger = useRef<HTMLElement | null>(null)

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("me.computers")} backTo="/me" />
      <MobileScrollArea storageKey="me-computers" ready={Boolean(resource.data)}>
        {resource.data ? (
          computers.length ? (
            <ul className="divide-y border-b">
              {computers.map((computer) => (
                <li key={computer.id} className="flex min-h-18 items-center gap-3 px-4 py-3">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
                    <LaptopIcon className="size-5" aria-hidden="true" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span
                      id={`mobile-computer-${computer.id}`}
                      className="flex min-w-0 items-center gap-2 text-[15px] font-medium"
                    >
                      <span className="min-w-0 truncate">
                        {computer.name}
                        <span className="font-normal text-muted-foreground">
                          {" · "}
                          {t(`settings:computers.platforms.${computer.platform}`)}
                        </span>
                      </span>
                      {computer.id === localComputerId ? (
                        <StatusBadge variant="muted">{t("settings:computers.list.current")}</StatusBadge>
                      ) : null}
                    </span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {presence(computer, formatDateTime)}
                    </span>
                  </span>
                  <Button
                    type="button"
                    variant="outline"
                    className="min-h-11 shrink-0"
                    disabled={revocation.pending}
                    aria-describedby={`mobile-computer-${computer.id}`}
                    onClick={(event) => {
                      trigger.current = event.currentTarget
                      revocation.select(computer)
                    }}
                  >
                    {t("settings:computers.revoke.action")}
                  </Button>
                </li>
              ))}
            </ul>
          ) : (
            <MobilePageState title={t("settings:computers.list.empty")} />
          )
        ) : resource.loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : resource.error ? (
          <MobilePageState
            title={t("settings:computers.list.loadError")}
            onRetry={() => void resource.refresh()}
          />
        ) : null}
      </MobileScrollArea>
      <ConfirmationDialog
        {...revocation.dialog}
        title={t("settings:computers.revoke.title", { name: revocation.item?.name ?? "" })}
        description={t("settings:computers.revoke.description")}
        pendingLabel={t("settings:computers.revoke.pending")}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          trigger.current?.focus({ preventScroll: true })
        }}
      />
    </section>
  )
}
