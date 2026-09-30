/** AI 写回复弹层的本机偏好、模式与选项控件和候选列表。 */
import { useState } from "react"
import { PencilLineIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ServiceReplyMode, ServiceReplyTone, type ServiceReplyAgent } from "@/api"
import { Button } from "@/components/ui/button"
import { NativeSelect } from "@/components/ui/native-select"
import { readLocalPreference, writeLocalPreference } from "@/lib/local-preference"
import { cn } from "@/lib/utils"

const replyModes = [
  ServiceReplyMode.ServiceReplyModeReply,
  ServiceReplyMode.ServiceReplyModeRewrite,
] as const

const replyTones = [
  ServiceReplyTone.ServiceReplyToneKeep,
  ServiceReplyTone.ServiceReplyToneProfessional,
  ServiceReplyTone.ServiceReplyToneFriendly,
  ServiceReplyTone.ServiceReplyToneConcise,
] as const

type ReplyAssistantPreferences = {
  tone: ServiceReplyTone
  agentIdentityId: string
}

/** 读取并更新本人在当前企业上次选择的语气和 AI 员工，未保存时使用默认值。 */
export function useReplyAssistantPreferences(currentIdentityID: string) {
  const storageKey = `app.inbox.replyAssistant.${currentIdentityID}`
  const [preferences, setPreferences] = useState<ReplyAssistantPreferences>(() => {
    const value = (readLocalPreference(storageKey) ?? {}) as Partial<Record<keyof ReplyAssistantPreferences, unknown>>
    return {
      tone: replyTones.find((tone) => tone === value.tone) ?? ServiceReplyTone.ServiceReplyToneKeep,
      agentIdentityId: typeof value.agentIdentityId === "string" ? value.agentIdentityId : "",
    }
  })

  /** 合并更新选择并在本机保存。 */
  function update(next: Partial<ReplyAssistantPreferences>) {
    const merged = { ...preferences, ...next }
    setPreferences(merged)
    writeLocalPreference(storageKey, merged)
  }

  return [preferences, update] as const
}

/** 写回复与改写两种模式的单选切换。 */
export function ReplyModeSelector({
  mode,
  mobile,
  onChange,
}: {
  mode: ServiceReplyMode
  mobile: boolean
  onChange: (mode: ServiceReplyMode) => void
}) {
  const { t } = useTranslation("inbox")
  const labels = {
    [ServiceReplyMode.ServiceReplyModeReply]: t("replyAssistantModeReply"),
    [ServiceReplyMode.ServiceReplyModeRewrite]: t("replyAssistantModeRewrite"),
  }
  return (
    <div
      role="radiogroup"
      aria-label={t("replyAssistantMode")}
      className={cn(
        "flex shrink-0 rounded-md border bg-background p-0.5",
        mobile && "w-full",
      )}
    >
      {replyModes.map((value) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={mode === value}
          className={cn(
            "rounded-sm font-medium whitespace-nowrap transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
            mobile ? "h-10 flex-1 text-sm" : "h-7 px-2.5 text-xs",
            mode === value
              ? "bg-foreground text-background"
              : "text-muted-foreground hover:bg-muted hover:text-foreground",
          )}
          onClick={() => onChange(value)}
        >
          {labels[value]}
        </button>
      ))}
    </div>
  )
}

/** 生成候选使用的 AI 员工；移动端由外部标签关联 id，桌面端使用 aria-label。 */
export function ReplyAgentSelect({
  id,
  agents,
  value,
  mobile,
  onChange,
}: {
  id: string
  agents: ServiceReplyAgent[]
  value: string
  mobile: boolean
  onChange: (agentIdentityId: string) => void
}) {
  const { t } = useTranslation("inbox")
  return (
    <NativeSelect
      id={mobile ? id : undefined}
      aria-label={mobile ? undefined : t("replyAssistantAgent")}
      className={cn(
        mobile
          ? "min-h-11 w-full text-sm"
          : "h-7 w-auto max-w-36 truncate px-2 pr-8 text-xs shadow-none",
      )}
      value={value}
      disabled={agents.length === 0}
      onChange={(event) => onChange(event.target.value)}
    >
      {agents.map((agent) => (
        <option key={agent.identityId} value={agent.identityId}>
          {agent.displayName}
        </option>
      ))}
    </NativeSelect>
  )
}

/** 候选回复的语气；移动端由外部标签关联 id，桌面端使用 aria-label。 */
export function ReplyToneSelect({
  id,
  value,
  mobile,
  onChange,
}: {
  id: string
  value: ServiceReplyTone
  mobile: boolean
  onChange: (tone: ServiceReplyTone) => void
}) {
  const { t } = useTranslation("inbox")
  const labels = {
    [ServiceReplyTone.ServiceReplyToneKeep]: t("replyAssistantToneKeep"),
    [ServiceReplyTone.ServiceReplyToneProfessional]: t("replyAssistantToneProfessional"),
    [ServiceReplyTone.ServiceReplyToneFriendly]: t("replyAssistantToneFriendly"),
    [ServiceReplyTone.ServiceReplyToneConcise]: t("replyAssistantToneConcise"),
  }
  return (
    <NativeSelect
      id={mobile ? id : undefined}
      aria-label={mobile ? undefined : t("replyAssistantTone")}
      className={cn(
        mobile ? "min-h-11 w-full text-sm" : "h-7 w-auto px-2 pr-8 text-xs shadow-none",
      )}
      value={value}
      onChange={(event) => onChange(event.target.value as ServiceReplyTone)}
    >
      {replyTones.map((tone) => (
        <option key={tone} value={tone}>
          {labels[tone]}
        </option>
      ))}
    </NativeSelect>
  )
}

/** 候选区：生成中显示占位，有候选时逐条列出供替换草稿，否则显示原因；failed 时说明以错误色显示。 */
export function ReplyCandidateList({
  generating,
  candidates,
  emptyMessage,
  failed,
  mobile,
  onApply,
}: {
  generating: boolean
  candidates: string[]
  emptyMessage: string
  failed: boolean
  mobile: boolean
  onApply: (candidate: string) => void
}) {
  const { t } = useTranslation("inbox")
  return (
    <div
      aria-live="polite"
      aria-busy={generating}
      className={cn(
        "overflow-y-auto",
        mobile ? "h-56 min-h-0 px-4" : "h-56 max-h-[calc(100dvh-17rem)] pr-1",
      )}
    >
      {generating ? (
        <div className="flex h-full items-center justify-center">
          <span className="sr-only">{t("replyAssistantGenerating")}</span>
          <div aria-hidden="true" className="w-28 space-y-2">
            <div className="h-2.5 w-full animate-pulse rounded bg-muted-foreground/25" />
            <div className="h-2.5 w-5/6 animate-pulse rounded bg-muted-foreground/20" />
            <div className="h-2.5 w-2/3 animate-pulse rounded bg-muted-foreground/15" />
          </div>
        </div>
      ) : candidates.length > 0 ? (
        <ul aria-label={t("replyAssistantCandidates")} className="space-y-2">
          {candidates.map((candidate, index) =>
            mobile ? (
              <li key={index}>
                <button
                  type="button"
                  className="w-full rounded-md border bg-background p-3 text-left text-sm leading-6 whitespace-pre-wrap outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-ring/50"
                  onClick={() => onApply(candidate)}
                >
                  {candidate}
                </button>
              </li>
            ) : (
              <li
                key={index}
                className="flex gap-2 rounded-md border bg-background p-2.5 text-sm leading-6"
              >
                <p className="min-w-0 flex-1 whitespace-pre-wrap">{candidate}</p>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="size-7 shrink-0 text-muted-foreground hover:text-foreground"
                  aria-label={t("replyAssistantApply")}
                  title={t("replyAssistantApply")}
                  onClick={() => onApply(candidate)}
                >
                  <PencilLineIcon className="size-4" />
                </Button>
              </li>
            ),
          )}
        </ul>
      ) : (
        <div
          className={cn(
            "flex h-full items-center justify-center rounded-md border border-dashed px-3 text-center text-xs text-muted-foreground",
            mobile && "text-sm",
            failed && "text-destructive",
          )}
        >
          {emptyMessage}
        </div>
      )}
    </div>
  )
}
