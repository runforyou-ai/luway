/** 添加渠道时选择平台的弹窗。 */
import { useTranslation } from "react-i18next"
import { Link } from "react-router"

import { StatusBadge } from "@/components/status-badge"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  messageChannelTypeDefinitions,
  plannedChannelDefinitions,
} from "@/lib/message-channel-types"
import { cn } from "@/lib/utils"

const cardClassName =
  "flex min-w-0 items-center gap-3 rounded-lg border bg-card p-4"
const iconTileClassName =
  "flex size-9 shrink-0 items-center justify-center rounded-lg [&>svg]:size-5"

/** 以卡片展示可接入的平台，选中后进入该平台的添加页；尚未接入的平台标注即将支持。 */
export function MessageChannelTypeDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation(["channels", "common"])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("list.create")}</DialogTitle>
          <DialogDescription>{t("list.typeDialogDescription")}</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {messageChannelTypeDefinitions.map((definition) => (
            <Link
              key={definition.type}
              to={`/channels/${definition.type}/new`}
              className={cn(
                cardClassName,
                "transition-colors hover:bg-accent/50 focus-visible:border-primary focus-visible:outline-hidden",
              )}
            >
              <span
                aria-hidden="true"
                className={cn(iconTileClassName, definition.softClassName)}
              >
                <definition.icon />
              </span>
              <span className="truncate text-sm font-medium">
                {t(`types.${definition.translationKey}`)}
              </span>
            </Link>
          ))}
          {plannedChannelDefinitions.map((definition) => (
            <div
              key={definition.key}
              aria-disabled="true"
              className={cn(cardClassName, "text-muted-foreground")}
            >
              <span
                aria-hidden="true"
                className={cn(iconTileClassName, "bg-muted")}
              >
                <definition.icon />
              </span>
              <span className="min-w-0 flex-1 truncate text-sm">
                {t(`plannedTypes.${definition.key}`)}
              </span>
              <StatusBadge variant="muted">{t("common:comingSoon")}</StatusBadge>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
