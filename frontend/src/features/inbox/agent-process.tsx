/** 展示 Agent 运行摘要与运行中的实时过程和任务清单，展开时读取思考过程与工具详情，并提供停止回复入口。 */
import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { useTheme } from "next-themes"
import { toast } from "sonner"
import { resourceKeys } from "@/hooks/resource-keys"
import { usePersonalAgentDisplayName } from "@/hooks/use-personal-agent-display-name"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { agentToolLabel } from "@/lib/agent-tool-labels"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { BrainIcon, ChevronDownIcon, CircleCheckIcon, CircleDotIcon, CircleIcon, LoaderCircleIcon, SquareIcon } from "lucide-react"
import { ThinkingOrb, type OrbState } from "thinking-orbs"
import { MessageMarkdown } from "@/components/message-markdown"
import { ProfileAvatar } from "@/components/profile-avatar"
import { openExternalURL } from "@/platform/external-navigation"
import { useTranslation } from "react-i18next"

import { useAgentRunStream } from "./use-agent-run-stream"
import {
  isApiError,
  currentDevice,
  getAgentRunProcess,
  stopAgentReply,
  stopServiceCopilotReply,
  stopGroupAgentReply,
  AgentPlanTaskStatus,
  AgentRunBlockKind,
  AgentRunStatus,
  AgentToolCallStatus,
  LocalToolchainFailure,
  LocalToolchainState,
  type ConversationAgentProcessData,
  type ConversationAgentRun,
  type ConversationPendingAgent,
  type RunStreamPlanTask,
  type RunStreamState,
  type RunStreamToolCall,
} from "@/api"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { AgentRunBlocks, isPlanToolBlock, useToolStatusLabel } from "@/components/agent-run-blocks"
import { cn } from "@/lib/utils"

/** 展示任务清单与完成进度，清单限高滚动；live 表示运行仍在进行，进行中的任务显示进行时说明；inBubble 表示位于消息气泡内，使用与气泡区分的底色。 */
function AgentPlan({ tasks, live, inBubble }: { tasks: RunStreamPlanTask[]; live: boolean; inBubble?: boolean }) {
  const { t } = useTranslation("inbox")
  const done = tasks.filter((task) => task.status === AgentPlanTaskStatus.AgentPlanTaskCompleted).length
  return (
    <section aria-label={t("agentPlanTitle")} className={cn("min-w-0 rounded-md px-3 py-2 text-xs text-foreground", inBubble ? "bg-background" : "bg-muted")}>
      <p className="mb-1.5 flex items-center justify-between gap-3 font-medium">
        <span>{t("agentPlanTitle")}</span>
        <span className="font-normal text-muted-foreground">{t("agentPlanProgress", { done, total: tasks.length })}</span>
      </p>
      <ol className="max-h-32 space-y-1 overflow-y-auto">
        {tasks.map((task) => {
          const completed = task.status === AgentPlanTaskStatus.AgentPlanTaskCompleted
          const active = task.status === AgentPlanTaskStatus.AgentPlanTaskInProgress
          const Icon = completed ? CircleCheckIcon : active ? (live ? LoaderCircleIcon : CircleDotIcon) : CircleIcon
          return (
            <li key={task.id} className="flex min-w-0 items-start gap-2 leading-5">
              <Icon aria-hidden className={cn("mt-0.5 size-3.5 shrink-0", active ? "text-primary" : "text-muted-foreground", active && live && "animate-spin")} />
              <span className={cn("min-w-0 break-words", completed && "text-muted-foreground line-through")}>
                <span className="sr-only">
                  {t(completed ? "agentPlanTaskCompleted" : active ? "agentPlanTaskInProgress" : "agentPlanTaskPending")}：
                </span>
                {active && live && task.activeForm ? task.activeForm : task.subject}
              </span>
            </li>
          )
        })}
      </ol>
    </section>
  )
}

/** 按所在位置的文字颜色和当前明暗主题绘制思考球体；paused 时固定为同一静止画面。 */
function AgentOrb({ state, paused }: { state: OrbState; paused?: boolean }) {
  const { resolvedTheme } = useTheme()
  const host = useRef<HTMLSpanElement>(null)
  const [color, setColor] = useState<string>()
  // 主题类名在同一次提交后才写入文档，下一帧读取文字颜色并经画布换算为 rgb。
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const context = document.createElement("canvas").getContext("2d", { willReadFrequently: true })
      if (!host.current || !context) return
      context.fillStyle = getComputedStyle(host.current).color
      context.fillRect(0, 0, 1, 1)
      const [red, green, blue] = context.getImageData(0, 0, 1, 1).data
      setColor(`rgb(${red},${green},${blue})`)
    })
    return () => cancelAnimationFrame(frame)
  }, [resolvedTheme])
  return (
    // 取得文字颜色前保留占位不显示，首帧不出现默认灰色。
    <span ref={host} aria-hidden className={cn("shrink-0", !color && "invisible")}>
      {/* 显式传入主题时球体不监听文档变化；速度为零的暂停帧固定为同一画面。 */}
      <ThinkingOrb state={state} size={20} theme={resolvedTheme === "dark" ? "dark" : "light"} color={color} speed={paused ? 0 : 1} paused={paused} />
    </span>
  )
}

/** 思考标题与过程内容在气泡内靠左排列，首次展开时按运行编号读取过程内容。onPrimary 表示内容位于主色气泡内，决定配色；inBubble 表示位于消息气泡内；onToggle 在展开或收起时暂停消息视口自动贴底。 */
export function AgentProcess({ process, onPrimary, inBubble, onToggle }: { process: ConversationAgentProcessData; onPrimary: boolean; inBubble?: boolean; onToggle: () => void }) {
  const { t } = useTranslation(["inbox", "common"])
  const [opened, setOpened] = useState(false)
  const seconds = Math.max(0, Math.round(process.durationMilliseconds / 1000))
  // 已完成运行的过程内容不可变，首次展开后按运行编号读取并长期复用缓存。
  const detail = useResource(
    resourceKeys.agentRunProcess(process.id),
    (signal) => getAgentRunProcess(process.id, signal),
    { enabled: opened, staleTime: Infinity },
  )
  return (
    <Collapsible className="mb-3 min-w-0" onOpenChange={(open) => { onToggle(); if (open) setOpened(true) }}>
      <CollapsibleTrigger className={cn(
        "group flex max-w-full min-w-0 cursor-pointer items-center justify-start gap-1.5 rounded-sm py-0.5 text-left text-xs focus-visible:outline focus-visible:outline-ring",
        onPrimary ? "text-accent-foreground/75" : "text-muted-foreground",
        // 移动端按触屏点击区域抬高行高，点击区不与引用块和正文重叠。
        "touch:py-2",
      )}>
        <AgentOrb state="working" paused />
        <span className="truncate">{t("agentThoughtCompleted", { seconds })}</span>
        <ChevronDownIcon aria-hidden className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-180" />
      </CollapsibleTrigger>
      <CollapsibleContent className={cn(
        "mt-2 space-y-3 border-l pl-3 text-left text-sm",
        onPrimary ? "border-accent-foreground/30" : "border-border",
      )}>
        {detail.data && detail.data.plan.length > 0 ? <AgentPlan tasks={detail.data.plan} live={false} inBubble={inBubble} /> : null}
        {detail.data ? (
          <AgentRunBlocks
            blocks={detail.data.blocks}
            thinkingClassName={onPrimary ? "text-accent-foreground/75" : "text-muted-foreground"}
            inBubble={inBubble}
            onToggle={onToggle}
          />
        ) : (
          // 读取中与读取失败共用一行占位，保持展开区域高度稳定。
          <p className={cn("text-xs", onPrimary ? "text-accent-foreground/75" : "text-muted-foreground")}>
            {detail.error && !detail.refreshing ? (
              <>
                <span>{isApiError(detail.error) ? apiErrorMessage(detail.error) : t("agentProcessLoadFailed")}</span>
                <button
                  type="button"
                  className="ml-2 rounded-sm underline focus-visible:outline focus-visible:outline-ring"
                  onClick={() => void detail.refresh()}
                >
                  {t("common:actions.retry")}
                </button>
              </>
            ) : t("common:status.loading")}
          </p>
        )}
      </CollapsibleContent>
    </Collapsible>
  )
}

/** 展示运行中工具调用的名称与状态，委派调用在名称下方显示子任务说明与子 Agent 正在使用的工具；完整参数和结果在运行成功后读取。 */
function AgentStreamTool({ call }: { call: RunStreamToolCall }) {
  const { t } = useTranslation("common")
  const statusLabel = useToolStatusLabel()
  const detail = [call.description, call.activity ? agentToolLabel(call.activity, t) : ""].filter(Boolean).join(" · ")
  return (
    <div className="flex min-w-0 items-center gap-3 rounded-md bg-muted px-3 py-2 text-xs text-foreground">
      <span className="min-w-0 flex-1">
        <span className="block break-all font-medium">{agentToolLabel(call.name, t)}</span>
        {detail ? <span className="mt-0.5 block break-all text-muted-foreground">{detail}</span> : null}
      </span>
      <span className={cn(
        "shrink-0 text-muted-foreground",
        call.status === AgentToolCallStatus.AgentToolCallFailed && "text-destructive",
      )}>
        {statusLabel(call.status)}
      </span>
    </div>
  )
}

/** 在顶部展示任务清单，其下按序渲染运行过程流中的思考、工具调用和正在生成的回复正文；过程区域限高滚动，内容增长时保持贴底。 */
function AgentRunStreamProcess({ state }: { state: RunStreamState }) {
  const { i18n } = useTranslation("inbox")
  const muted = "text-muted-foreground"
  const scroll = useRef<HTMLDivElement>(null)
  const following = useRef(true)
  useLayoutEffect(() => {
    const node = scroll.current
    if (node && following.current) node.scrollTop = node.scrollHeight
  }, [state])
  return (
    <div className="space-y-3">
      {state.plan.length > 0 ? <AgentPlan tasks={state.plan} live /> : null}
      <div
        ref={scroll}
        // 限高让状态行与停止按钮始终可见；用户上滚查看早先过程时停止自动贴底。
        className="max-h-64 space-y-3 overflow-y-auto"
        onScroll={(event) => {
          const node = event.currentTarget
          following.current = node.scrollHeight - node.scrollTop - node.clientHeight <= 16
        }}
      >
        {state.blocks.filter((block) => !isPlanToolBlock(block)).map((block) =>
          block.kind === AgentRunBlockKind.AgentRunBlockToolCall && block.toolCall ? (
            <AgentStreamTool key={block.id} call={block.toolCall} />
          ) : (
            <div
              key={block.id}
              className={cn("min-w-0 break-words", block.kind === AgentRunBlockKind.AgentRunBlockThinking && cn("italic", muted))}
            >
              <MessageMarkdown locale={i18n.language} onOpenLink={openExternalURL}>{block.text}</MessageMarkdown>
            </div>
          ),
        )}
        {state.candidateContent ? (
          <div className="min-w-0 break-words">
            <MessageMarkdown locale={i18n.language} onOpenLink={openExternalURL}>{state.candidateContent}</MessageMarkdown>
          </div>
        ) : null}
      </div>
    </div>
  )
}

/** 显示一次尚未由消息表达的运行的等待、思考或取消状态，运行中按当前阶段展示动画与文案并可展开实时过程，取消运行可展开中断前的过程。 */
export function AgentRunState({ run, incoming, conversationID, group, copilot, onStopped, onToggle }: { run: ConversationAgentRun; incoming: boolean; conversationID?: string; group?: boolean; copilot?: boolean; onStopped: () => Promise<unknown>; onToggle: () => void }) {
  const { t } = useTranslation("inbox")
  const { t: tCommon } = useTranslation("common")
  const personalAgentDisplayName = usePersonalAgentDisplayName()
  const stream = useAgentRunStream(run.id, run.status === AgentRunStatus.AgentRunStatusRunning, onStopped)
  // 排队等待本机执行时读取本机运行环境，未就绪时说明正在准备或准备失败。
  const queuedOnDevice = run.status === AgentRunStatus.AgentRunStatusQueued && run.executionDeviceId != null
  const { data: localDevice } = useResource(resourceKeys.currentDevice(), () => currentDevice(), { enabled: queuedOnDevice })
  const toolchain = queuedOnDevice && localDevice?.deviceId === run.executionDeviceId ? localDevice.toolchain : null
  if (run.status === AgentRunStatus.AgentRunStatusSucceeded || run.status === AgentRunStatus.AgentRunStatusFailed ||
    (run.status === AgentRunStatus.AgentRunStatusCancelled && run.errorCode === "user_cancelled")) return null
  const thinking = run.status === AgentRunStatus.AgentRunStatusRunning
  const cancelled = run.status === AgentRunStatus.AgentRunStatusCancelled
  // 按实时过程判断当前阶段：正文生成中为撰写；存在未结束且已有名称的非任务清单工具时，取最近发起的一个为使用工具；其余为思考。
  const activeToolName = stream && !stream.candidateContent
    ? [...stream.blocks].reverse().map((block) =>
      block.toolCall && !isPlanToolBlock(block) &&
      (block.toolCall.status === AgentToolCallStatus.AgentToolCallQueued || block.toolCall.status === AgentToolCallStatus.AgentToolCallRunning)
        ? agentToolLabel(block.toolCall.activity || block.toolCall.name, tCommon)
        : "",
    ).find(Boolean)
    : undefined
  const phase: OrbState = stream?.candidateContent ? "composing" : activeToolName ? "searching" : "working"
  const senderName = personalAgentDisplayName(run.agentName.trim(), run.agentPersonalResponsibleName) || t("unknownSender")
  const label = thinking
    ? stream?.candidateContent
      ? t("agentRunComposing")
      : activeToolName
        ? t("agentRunUsingTool", { tool: activeToolName })
        : t("agentThoughtRunning")
    : cancelled
      ? t("agentRunCancelled")
      : toolchain?.state === LocalToolchainState.LocalToolchainStatePreparing
        ? t("agentRunPreparingToolchain")
        : toolchain?.state === LocalToolchainState.LocalToolchainStateFailed
          ? toolchain.failure === LocalToolchainFailure.LocalToolchainFailureDownload
            ? t("agentRunToolchainDownloadFailed")
            : toolchain.failure === LocalToolchainFailure.LocalToolchainFailureVerify
              ? t("agentRunToolchainVerifyFailed")
              : t("agentRunToolchainInstallFailed")
          : run.executionDeviceName
            ? t("agentRunQueuedOnDevice", { name: run.executionDeviceName })
            : t("agentRunQueued")
  const reason = run.errorCode === "assignee_changed"
    ? t("agentRunAssigneeChanged")
    : run.errorCode === "session_closed"
      ? t("agentRunSessionClosed")
      : run.errorCode === "bot_changed"
        ? t("agentRunBotChanged")
        : run.errorCode === "agent_removed"
          ? t("agentRunAgentRemoved")
          : run.errorCode === "agent_unavailable"
            ? t("agentUnavailable")
            : run.lastError
  return (
    <div
      className={cn("mt-3 flex min-w-0 text-xs text-muted-foreground", incoming ? "justify-start" : "justify-end")}
      role="status"
      aria-label={`${senderName} ${label}`}
    >
      <div className={cn(
        "relative flex min-h-8 max-w-[75%] flex-col justify-center py-2",
        // 运行中的过程与最终消息气泡同宽，结束后替换为消息时保持原有换行。
        thinking && "max-w-[min(36rem,85%)] sm:max-w-[min(36rem,75%)]",
        // Copilot 面板与其最终消息一致，不展示头像，在上方标出发送者。
        !copilot && (incoming ? "ml-9" : "mr-9"),
      )}>
        {copilot ? null : (
          <ProfileAvatar
            imageURL={run.agentAvatarUrl}
            name={run.agentName}
            fallback="agent"
            title={senderName}
            className={cn(
              "absolute bottom-0 size-7",
              incoming ? "right-full mr-2" : "left-full ml-2",
            )}
          />
        )}
        {(group || copilot) && incoming ? (
          <span className="mb-1 max-w-full truncate text-xs font-medium text-foreground">{senderName}</span>
        ) : null}
        {thinking ? (
          <Collapsible className="min-w-0">
            <div className={cn(
              "flex items-center gap-1.5",
              // 状态行贴头像侧对齐，展开过程使区域变宽时标题与停止按钮位置不变。
              !incoming && "justify-end",
              // 移动端留出停止按钮触屏区域向左溢出的宽度，两个点击区域不重叠。
              "touch:gap-3",
            )}>
              <CollapsibleTrigger className={cn(
                "group flex min-w-0 cursor-pointer items-center gap-1.5 rounded-sm text-left focus-visible:outline focus-visible:outline-ring",
                // 移动端按触屏点击区域抬高行高，点击区不与展开的过程内容重叠。
                "touch:py-2",
              )}>
                <AgentOrb state={phase} />
                <span className="truncate">{label}</span>
                <ChevronDownIcon aria-hidden className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-180" />
              </CollapsibleTrigger>
              {conversationID ? <AgentReplyStopButton conversationID={conversationID} runID={run.id} group={group} copilot={copilot} onStopped={onStopped} /> : null}
            </div>
            <CollapsibleContent className={cn(
              "text-left text-sm",
              // 首个快照到达前不占位，展开区域不出现空的缩进边框。
              stream && cn("mt-2 border-border", incoming ? "border-l pl-3" : "border-r pr-3"),
            )}>
              {stream ? <AgentRunStreamProcess state={stream} /> : null}
            </CollapsibleContent>
          </Collapsible>
        ) : (
          <>
            {run.process ? <AgentProcess process={run.process} onPrimary={false} onToggle={onToggle} /> : null}
            <div className="flex items-center gap-1.5">
              {cancelled ? <BrainIcon aria-hidden className="size-4" /> : toolchain?.state === LocalToolchainState.LocalToolchainStateFailed ? null : <AgentOrb state="breathing" />}
              <span>{label}</span>
              {conversationID && !cancelled ? <AgentReplyStopButton conversationID={conversationID} runID={run.id} group={group} copilot={copilot} onStopped={onStopped} /> : null}
            </div>
          </>
        )}
        {cancelled && reason ? (
          <p className="mt-1 whitespace-pre-wrap break-all">{reason}</p>
        ) : null}
      </div>
    </div>
  )
}

/** 展示已收到点名、等待轮转发言的 AI 员工。 */
export function AgentQueueState({ agents, incoming, copilot }: { agents: ConversationPendingAgent[]; incoming: boolean; copilot?: boolean }) {
  const { t } = useTranslation("inbox")
  const personalAgentDisplayName = usePersonalAgentDisplayName()
  if (!agents.length) return null
  const names = agents.map((agent) => personalAgentDisplayName(agent.displayName.trim(), agent.personalResponsibleName) || t("unknownSender")).join("、")
  return (
    <div
      className={cn("mt-2 flex min-w-0 text-xs text-muted-foreground", incoming ? "justify-start" : "justify-end")}
      role="status"
    >
      <span className={cn("max-w-[75%] break-all", !copilot && (incoming ? "ml-9" : "mr-9"))}>
        {t("agentRunWaiting", { names })}
      </span>
    </div>
  )
}

/** 停止指定运行后刷新原会话资源，卸载后忽略交互结果。 */
function AgentReplyStopButton({ conversationID, runID, group, copilot, onStopped }: { conversationID: string; runID: string; group?: boolean; copilot?: boolean; onStopped: () => Promise<unknown> }) {
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const [stopping, setStopping] = useState(false)
  const alive = useMountedRef()

  /** 提交停止命令并通过查询读取最终状态和消息。 */
  async function stop() {
    setStopping(true)
    try {
      // 按会话类型选择 Copilot 线程、群聊或独立 AI 会话的停止入口。
      if (copilot) await stopServiceCopilotReply(conversationID, runID)
      else if (group) await stopGroupAgentReply(conversationID, runID)
      else await stopAgentReply(conversationID, runID)
      await Promise.all([
        invalidate(resourceKeys.conversationMessages(conversationID)),
        invalidate(resourceKeys.conversationMessagePage(conversationID)),
        invalidate(resourceKeys.inbox()),
      ])
      if (alive.current) await onStopped()
    } catch (error) {
      if (!alive.current || recoverSession(error, navigate)) return
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("agentStopFailed"))
    } finally {
      if (alive.current) setStopping(false)
    }
  }

  return (
    <button
      type="button"
      className={cn(
        "inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-destructive focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50",
        // 移动端扩大触屏点击区域，负外边距保持原有行高和图标位置。
        "touch:-m-3 touch:size-11",
      )}
      aria-label={t("agentStopReply")}
      title={t("agentStopReply")}
      disabled={stopping}
      onClick={() => void stop()}
    >
      <SquareIcon aria-hidden className="size-3 fill-current" />
    </button>
  )
}
