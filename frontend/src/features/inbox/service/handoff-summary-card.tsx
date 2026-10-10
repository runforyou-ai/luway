/** 服务会话顶部的 AI 交接卡片：发起人诉求、AI 已完成的处理与需要人工处理的卡点，可定位到转人工的位置，真人领取后收起。 */
import { useState } from "react"
import { ChevronDownIcon, ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { WorkspaceIdentityType, type InboxAssignee } from "@/api"

import { useServiceSummaries } from "@/features/inbox/shared/service-session-summary"

/** 当前周期有交接摘要时显示卡片；无人负责时默认展开，真人负责时默认收起，可手动切换；展开后可在时间线中定位转人工事件。 */
export function HandoffSummaryCard({
  conversationID,
  assignee,
  onLocateMessage,
}: {
  conversationID: string
  assignee: InboxAssignee | null
  onLocateMessage: (messageID: string) => void
}) {
  const { t } = useTranslation("inbox")
  const claimed = assignee?.type === WorkspaceIdentityType.User
  const [toggled, setToggled] = useState<{ claimed: boolean; expanded: boolean } | null>(null)
  const summaries = useServiceSummaries(conversationID)
  const handoff = summaries.data?.handoff
  if (!handoff) return null
  // 负责人变化后恢复默认展开状态。
  const expanded = toggled && toggled.claimed === claimed ? toggled.expanded : !claimed
  const Icon = expanded ? ChevronDownIcon : ChevronRightIcon
  return (
    <section className="mx-3 mt-2 shrink-0 rounded-lg border bg-muted/30 px-3 py-2 text-sm">
      <button
        type="button"
        className="flex w-full min-w-0 items-center gap-1.5 text-left"
        aria-expanded={expanded}
        onClick={() => setToggled({ claimed, expanded: !expanded })}
      >
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <span className="shrink-0 font-medium">{t("handoffSummaryTitle")}</span>
        {expanded ? null : <span className="min-w-0 truncate text-muted-foreground">{handoff.request}</span>}
      </button>
      {expanded ? (
        <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 pl-5.5">
          {(
            [
              ["handoffSummaryRequest", handoff.request],
              ["handoffSummaryProgress", handoff.progress],
              ["handoffSummaryBlocker", handoff.blocker],
            ] as const
          ).map(([label, value]) =>
            value ? (
              <div key={label} className="contents">
                <dt className="text-muted-foreground">{t(label)}</dt>
                <dd className="break-words">{value}</dd>
              </div>
            ) : null,
          )}
          {handoff.messageId ? (
            <dd className="col-start-2">
              <button
                type="button"
                className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                onClick={() => onLocateMessage(handoff.messageId)}
              >
                {t("handoffSummaryLocate")}
              </button>
            </dd>
          ) : null}
        </dl>
      ) : null}
    </section>
  )
}
