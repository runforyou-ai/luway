/** 展示交给本机 Agent 的一轮：执行状态、停止入口与实时的回复、思考、步骤、文件差异和任务清单。 */
import { useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { ChevronDownIcon, CircleCheckIcon, CircleIcon, CircleXIcon, LoaderCircleIcon, SquareIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  type AgentPlanTaskStatus,
  AgentToolCallStatus,
  AgentToolCallUpdateKind,
  getAgentToolCallProcess,
  stopLocalAgent,
  type AgentToolCallProcess,
  type AgentToolCallStep,
} from "@/api"
import { isUnsettledToolCall, useToolStatusLabel } from "@/components/agent-run-blocks"
import { MessageMarkdown } from "@/components/message-markdown"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { resourceKeys } from "@/hooks/resource-keys"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { cn } from "@/lib/utils"
import { openExternalURL } from "@/platform/system"

import { AgentPlan } from "./agent-process"

/** 本机 Agent 过程中的一项展示内容：一段回复或思考，或一个步骤的最新状态。 */
export type LocalAgentItem =
  | { kind: "message" | "thought"; text: string }
  | { kind: "step"; step: AgentToolCallStep }

/** 把按序号排列的过程更新整理为展示内容：连续的回复或思考片段拼接为一段，同一步骤保留首次出现的位置并取最新状态，任务清单取最新一份。 */
export function localAgentView(updates: AgentToolCallProcess["updates"]) {
  const items: LocalAgentItem[] = []
  const steps = new Map<string, number>()
  let plan: { content: string; status: string }[] = []
  for (const update of updates) {
    if (update.kind === AgentToolCallUpdateKind.AgentToolCallUpdateMessage || update.kind === AgentToolCallUpdateKind.AgentToolCallUpdateThought) {
      const kind = update.kind === AgentToolCallUpdateKind.AgentToolCallUpdateMessage ? "message" : "thought"
      const last = items[items.length - 1]
      if (last && last.kind === kind) last.text += update.text
      else items.push({ kind, text: update.text })
    } else if (update.kind === AgentToolCallUpdateKind.AgentToolCallUpdateStep && update.step) {
      const index = steps.get(update.step.id)
      if (index === undefined) {
        steps.set(update.step.id, items.length)
        items.push({ kind: "step", step: update.step })
      } else {
        items[index] = { kind: "step", step: update.step }
      }
    } else if (update.kind === AgentToolCallUpdateKind.AgentToolCallUpdatePlan) {
      plan = update.plan ?? []
    }
  }
  return { items, plan }
}

/** 展示一个步骤的标题、状态与涉及的文件，有文件差异或输出时可以展开查看。 */
function LocalAgentStep({ step, live }: { step: AgentToolCallStep; live: boolean }) {
  const { t } = useTranslation(["inbox", "common"])
  const failed = step.status === "failed"
  const done = step.status === "completed"
  const Icon = failed ? CircleXIcon : done ? CircleCheckIcon : live ? LoaderCircleIcon : CircleIcon
  const content = step.content ?? []
  const header = (
    <span className="flex min-w-0 flex-1 items-start gap-2">
      <Icon aria-hidden className={cn("mt-0.5 size-3.5 shrink-0", failed ? "text-destructive" : "text-muted-foreground", !failed && !done && live && "animate-spin")} />
      <span className="min-w-0 flex-1">
        <span className="block break-all font-medium">{step.title || t("localAgentStepUntitled")}</span>
        {step.locations?.length ? <span className="mt-0.5 block break-all text-muted-foreground">{step.locations.join("、")}</span> : null}
      </span>
    </span>
  )
  if (content.length === 0) {
    return <div className="flex min-w-0 items-start gap-3 rounded-md bg-muted px-3 py-2 text-xs">{header}</div>
  }
  return (
    <Collapsible className="min-w-0 rounded-md bg-muted text-xs">
      <CollapsibleTrigger className="group flex w-full min-w-0 items-start gap-3 rounded-md px-3 py-2 text-left focus-visible:outline focus-visible:outline-ring">
        {header}
        <ChevronDownIcon aria-hidden className="mt-0.5 size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-180" />
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-2 px-3 pb-3">
        {content.map((item, index) => item.diff ? (
          <div key={index} className="min-w-0">
            <p className="mb-1 break-all text-muted-foreground">
              {t(item.diff.oldText === null ? "common:localAgentFile.created" : "common:localAgentFile.changed", { path: item.diff.path })}
            </p>
            <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-all rounded-sm bg-background p-2 font-mono leading-5">{item.diff.newText}</pre>
          </div>
        ) : (
          <pre key={index} className="max-h-60 overflow-auto whitespace-pre-wrap break-all font-mono leading-5">{item.text}</pre>
        ))}
      </CollapsibleContent>
    </Collapsible>
  )
}

/** 交给本机 Agent 的一轮卡片：标题显示本机 Agent 与执行状态，执行中可以停止；展开后按序展示过程，执行中默认展开；过程按调用编号读取，电脑上报新过程时经实时通知重读，执行中另每 5 秒重读一次；onToggle 在展开或收起时暂停消息视口自动贴底。 */
export function LocalAgentTurn({ toolCallId, localAgent, conversationId, onToggle }: { toolCallId: string; localAgent: string; conversationId: string; onToggle?: () => void }) {
  const { t, i18n } = useTranslation(["inbox", "common"])
  const statusLabel = useToolStatusLabel()
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  const [opened, setOpened] = useState<boolean | null>(null)
  const process = useResource(
    resourceKeys.agentToolCallProcess(toolCallId),
    (signal) => getAgentToolCallProcess(toolCallId, signal),
    { staleTime: 0, refetchInterval: (data) => (data && isUnsettledToolCall(data) ? 5000 : false) },
  )
  const stop = useMutation({
    mutationFn: () => stopLocalAgent(toolCallId),
    onError: (error) => reportError(error, { log: "停止本机 Agent", context: { toolCallId }, fallback: t("localAgentStopFailed") }),
    onSettled: () => {
      void invalidate(resourceKeys.agentToolCallProcess(toolCallId))
      void invalidate(resourceKeys.conversationMessages(conversationId))
      void invalidate(resourceKeys.conversationMessagePage(conversationId))
    },
  })
  const data = process.data
  const live = !!data && isUnsettledToolCall(data)
  const open = opened ?? live
  const view = data ? localAgentView(data.updates) : { items: [], plan: [] }
  const alerting = data?.status === AgentToolCallStatus.AgentToolCallFailed
  return (
    <Collapsible
      open={open}
      onOpenChange={(next) => {
        onToggle?.()
        setOpened(next)
      }}
      className="mt-3 min-w-0 rounded-md bg-background text-left text-foreground"
    >
      <div className="flex min-w-0 items-center gap-2 px-3 py-2 text-xs">
        <CollapsibleTrigger className="group flex min-w-0 flex-1 items-center gap-2 rounded-sm text-left focus-visible:outline focus-visible:outline-ring">
          <span className="min-w-0 flex-1 truncate font-medium">{t("localAgentTurnTitle", { agent: localAgent })}</span>
          <span className={cn("shrink-0 text-muted-foreground", alerting && "text-destructive")}>
            {data ? statusLabel(data.status) : t("common:status.loading")}
          </span>
          <ChevronDownIcon aria-hidden className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-180" />
        </CollapsibleTrigger>
        {live ? (
          <button
            type="button"
            className={cn(
              "inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-destructive focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50",
              // 移动端扩大触屏点击区域，负外边距保持原有行高和图标位置。
              "touch:-m-3 touch:size-11",
            )}
            aria-label={t("localAgentStop", { agent: localAgent })}
            title={t("localAgentStop", { agent: localAgent })}
            disabled={stop.isPending}
            onClick={() => stop.mutate()}
          >
            <SquareIcon aria-hidden className="size-3 fill-current" />
          </button>
        ) : null}
      </div>
      <CollapsibleContent className="space-y-2 px-3 pb-3 text-sm">
        {view.plan.length > 0 ? (
          <AgentPlan
            tasks={view.plan.map((entry, index) => ({ id: String(index), subject: entry.content, status: entry.status as AgentPlanTaskStatus }))}
            live={live}
          />
        ) : null}
        {view.items.map((item, index) => item.kind === "step" ? (
          <LocalAgentStep key={item.step.id} step={item.step} live={live} />
        ) : (
          <div key={index} className={cn("min-w-0 break-words", item.kind === "thought" && "italic text-muted-foreground")}>
            <MessageMarkdown locale={i18n.language} onOpenLink={openExternalURL}>{item.text}</MessageMarkdown>
          </div>
        ))}
        {data && view.items.length === 0 && view.plan.length === 0 ? (
          <p className="text-xs text-muted-foreground">{live ? t("localAgentWaiting") : t("localAgentNoProcess")}</p>
        ) : null}
        {data?.error ? <p className="break-words text-xs text-destructive">{data.error}</p> : null}
      </CollapsibleContent>
    </Collapsible>
  )
}
