/** 收件箱列表列的顶部操作行。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { InboxScope } from "@/api"
import { CountBadge } from "@/components/count-badge"
import { inboxTabs, type InboxTab } from "@/features/inbox/inbox-query"
import { cn } from "@/lib/utils"

/** 顶部操作行：待处理与全部两个等宽分段页签占满行宽，右侧为当前页签的筛选。 */
export function InboxPaneTop({
  tab,
  pendingCount,
  onTabChange,
  filter,
}: {
  tab: InboxTab
  pendingCount: number
  onTabChange: (tab: InboxTab) => void
  filter: ReactNode
}) {
  const { t } = useTranslation("inbox")

  return (
    <div
      data-slot="inbox-pane-header"
      className="flex h-12 shrink-0 items-center gap-1.5 px-2.5"
    >
      <nav
        aria-label={t("tabLabel")}
        className="flex h-8 min-w-0 flex-1 items-center gap-0.5 rounded-lg bg-muted p-0.5"
      >
        {inboxTabs.map((item) => (
          <button
            key={item.value}
            type="button"
            aria-pressed={tab === item.value}
            className={cn(
              "flex h-7 min-w-0 flex-1 items-center justify-center gap-1.5 rounded-md px-2 text-sm text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
              tab === item.value && "bg-background font-medium text-foreground shadow-sm",
            )}
            onClick={() => onTabChange(item.value)}
          >
            <span className="truncate">{t(item.label)}</span>
            {item.value === InboxScope.InboxScopePending && pendingCount > 0 ? (
              <CountBadge count={pendingCount} tone="neutral" label={t("pendingCount", { count: pendingCount })} />
            ) : null}
          </button>
        ))}
      </nav>
      <div className="flex shrink-0 items-center">{filter}</div>
    </div>
  )
}
