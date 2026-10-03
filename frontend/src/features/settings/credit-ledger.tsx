/** 积分余额概览与积分流水表格，工作区积分页与平台管理员查看工作区积分时共用。 */
import { GiftIcon, HandCoinsIcon, HourglassIcon, SparklesIcon, Undo2Icon, WalletIcon, type LucideIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { AIModelCallStatus, CreditEntryKind, type CreditBalance, type CreditEntryData } from "@/api"
import { StatTile } from "@/components/report-parts"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { useCreditFormat } from "@/hooks/use-credit-format"
import { useDateTime } from "@/hooks/use-date-time"

/** 各流水类型的行首图标。 */
const entryIcons: Record<CreditEntryData["kind"], LucideIcon> = {
  [CreditEntryKind.CreditEntryKindDailyGrant]: GiftIcon,
  [CreditEntryKind.CreditEntryKindAdjustment]: HandCoinsIcon,
  [CreditEntryKind.CreditEntryKindModelCall]: SparklesIcon,
  [CreditEntryKind.CreditEntryKindExpiration]: HourglassIcon,
  [CreditEntryKind.CreditEntryKindPurchase]: WalletIcon,
  [CreditEntryKind.CreditEntryKindRefund]: Undo2Icon,
}

/** 展示可用积分与今天的每日赠送剩余及清零时间。 */
export function CreditBalanceTiles({ balance }: { balance: CreditBalance }) {
  const { t } = useTranslation("settings")
  const credit = useCreditFormat()
  const { formatDateTime } = useDateTime()
  return (
    <div className="grid grid-cols-2 gap-3">
      <StatTile
        label={t("credits.available")}
        value={credit.amount(balance.available)}
        detail={balance.dailyGrant > 0 ? t("credits.dailyGrant", { amount: credit.amount(balance.dailyGrant) }) : t("credits.noDailyGrant")}
      />
      <StatTile
        label={t("credits.dailyGrantRemaining")}
        value={credit.amount(balance.dailyGrantRemaining)}
        detail={
          balance.dailyGrantExpiresAt
            ? t("credits.dailyGrantExpires", { time: formatDateTime(balance.dailyGrantExpiresAt) })
            : t("credits.noDailyGrantToday")
        }
      />
    </div>
  )
}

/** 列出积分流水：主行为事件名称与补充信息，模型调用第二行为 Token 用量，进行中的调用标记为预占，积分变动与时间独立成列。 */
export function CreditEntryTable({ entries }: { entries: readonly CreditEntryData[] }) {
  const { t } = useTranslation(["settings", "common"])
  const credit = useCreditFormat()
  const { formatDateTime } = useDateTime()
  return (
    <ResourceTable<CreditEntryData>
      columns={[
        {
          key: "entry",
          header: t("credits.entries.title"),
          cellClassName: "min-w-0",
          cell: (entry) =>
            entry.kind === CreditEntryKind.CreditEntryKindModelCall ? (
              <ResourceRowIdentity
                icon={entryIcons[entry.kind]}
                name={entry.modelName}
                secondary={t(`common:aiModels.usages.${entry.modelUsage}`)}
                badge={
                  entry.callStatus === AIModelCallStatus.AIModelCallStatusRunning ? (
                    <StatusBadge variant="warning">{t("credits.entries.reserved")}</StatusBadge>
                  ) : undefined
                }
                description={t("common:aiModels.tokens", {
                  input: entry.inputTokens.toLocaleString(),
                  output: entry.outputTokens.toLocaleString(),
                })}
              />
            ) : (
              <ResourceRowIdentity
                icon={entryIcons[entry.kind]}
                name={t(`credits.entries.kinds.${entry.kind}`)}
                secondary={entry.note || undefined}
              />
            ),
        },
        {
          key: "amount",
          header: t("credits.entries.amount"),
          cellClassName: "w-px whitespace-nowrap text-right tabular-nums",
          cell: (entry) => (
            <span className={entry.amount > 0 ? "font-medium text-foreground" : "text-muted-foreground"}>{credit.change(entry.amount)}</span>
          ),
        },
        {
          key: "time",
          header: t("credits.entries.time"),
          cellClassName: "hidden w-px whitespace-nowrap text-right text-muted-foreground tabular-nums sm:table-cell",
          cell: (entry) => formatDateTime(entry.occurredAt),
        },
      ]}
      rows={entries}
      rowKey={(entry) => `${entry.kind}:${entry.id}`}
      empty={t("credits.entries.empty")}
    />
  )
}
