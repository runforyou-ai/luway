/** Agent 运行过程的内容块展示：文本、思考与工具调用，以及工具参数和结果的展开查看。 */
import { useLayoutEffect, useRef, useState, type ReactNode } from "react"
import { ChevronDownIcon } from "lucide-react"
import { Popover } from "radix-ui"
import { useTranslation } from "react-i18next"

import {
  AgentRunBlockKind,
  AgentToolCallStatus,
  type AgentRunContentBlock,
  type AgentToolCall,
} from "@/api"
import { MessageMarkdown } from "@/components/message-markdown"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { usePortalContainer } from "@/components/ui/portal-container"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { agentToolLabel } from "@/lib/agent-tool-labels"
import { cn } from "@/lib/utils"
import { openExternalURL } from "@/platform/external-navigation"

/** 任务清单工具的名称，调用成功时由任务清单统一展示，不逐条列出。 */
const planToolNames = new Set(["TaskCreate", "TaskGet", "TaskUpdate", "TaskList"])

/** 判断内容块是否为调用未失败的任务清单工具，失败的调用保留在过程中。 */
export function isPlanToolBlock(block: { toolCall?: { name: string; status: AgentToolCallStatus } | null }) {
  return !!block.toolCall && planToolNames.has(block.toolCall.name) && block.toolCall.status !== AgentToolCallStatus.AgentToolCallFailed
}

/** 委派子任务的工具名称。 */
const delegateToolName = "agent"

/** 在截断末尾提供更多按钮，点击后浮层展示完整原文；surface 为所在卡片底色，用于遮住截断处的原文。 */
function ToolValue({ value, surface }: { value: string; surface: string }) {
  const { t } = useTranslation("inbox")
  const pagePortal = usePortalContainer()
  const element = useRef<HTMLPreElement>(null)
  const [truncated, setTruncated] = useState(false)
  useLayoutEffect(() => {
    const node = element.current
    if (!node) return
    const measure = () => setTruncated(
      node.scrollHeight > node.clientHeight || node.scrollWidth > node.clientWidth,
    )
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(node)
    return () => observer.disconnect()
  }, [value])

  return (
    <div className="relative min-w-0">
      <pre
        ref={element}
        className="max-h-20 overflow-hidden whitespace-pre-wrap break-all font-mono text-xs leading-5"
      >
        {value}
      </pre>
      {truncated && (pagePortal?.active ?? true) ? (
        <Popover.Root>
          <span className={cn("absolute right-0 bottom-0 flex items-center gap-1 pl-1 text-xs leading-5", surface)}>
            <span aria-hidden="true">…</span>
            <Popover.Trigger asChild>
              <button
                type="button"
                className="rounded-sm text-muted-foreground hover:underline focus-visible:outline focus-visible:outline-ring"
              >
                {t("agentToolMore")}
              </button>
            </Popover.Trigger>
          </span>
          <Popover.Portal container={pagePortal?.container}>
            <Popover.Content
              side="top"
              align="end"
              sideOffset={6}
              collisionPadding={16}
              aria-label={t("agentToolFullContent")}
              className="z-50 max-h-[min(20rem,var(--radix-popover-content-available-height))] w-max max-w-[min(40rem,calc(100vw-2rem))] overflow-y-auto rounded-md bg-foreground px-3 py-2 text-left font-mono text-xs leading-5 whitespace-pre-wrap break-all text-background shadow-md outline-none"
            >
              {value}
              <Popover.Arrow className="fill-foreground" />
            </Popover.Content>
          </Popover.Portal>
        </Popover.Root>
      ) : null}
    </div>
  )
}

/** 返回工具执行状态文案。 */
export function useToolStatusLabel() {
  const { t } = useTranslation("inbox")
  return (status: AgentToolCallStatus) => ({
    [AgentToolCallStatus.AgentToolCallQueued]: t("agentToolQueued"),
    [AgentToolCallStatus.AgentToolCallRunning]: t("agentToolRunning"),
    [AgentToolCallStatus.AgentToolCallSucceeded]: t("agentToolSucceeded"),
    [AgentToolCallStatus.AgentToolCallFailed]: t("agentToolFailed"),
  })[status]
}

/** 展示单次工具调用并按需展开完整参数、结果或错误，detail 显示在工具名下方；inBubble 表示位于消息气泡内，使用与气泡区分的底色。 */
export function AgentTool({ call, detail, inBubble, onToggle }: { call: AgentToolCall; detail?: ReactNode; inBubble?: boolean; onToggle?: () => void }) {
  const { t } = useTranslation("inbox")
  const { t: tCommon } = useTranslation("common")
  const failed = call.status === AgentToolCallStatus.AgentToolCallFailed
  const statusLabel = useToolStatusLabel()(call.status)
  const surface = inBubble ? "bg-background" : "bg-muted"
  return (
    <Collapsible className={cn("min-w-0 rounded-md text-foreground", surface)} onOpenChange={onToggle}>
      <CollapsibleTrigger className="group flex w-full min-w-0 items-center gap-3 rounded-md px-3 py-2 text-left text-xs focus-visible:outline focus-visible:outline-ring">
        <span className="min-w-0 flex-1">
          <span className="block break-all font-medium">{agentToolLabel(call.name, tCommon)}</span>
          {detail ? <span className="mt-0.5 block text-muted-foreground">{detail}</span> : null}
        </span>
        <span className={cn("shrink-0 text-muted-foreground", failed && "text-destructive")}>
          {statusLabel}
        </span>
        <ChevronDownIcon aria-hidden className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-180" />
      </CollapsibleTrigger>
      <CollapsibleContent className="px-3 pb-3">
        <Tabs defaultValue="arguments">
          <TabsList className="gap-4">
            <TabsTrigger value="arguments" className="pb-2 text-xs">
              {t("agentToolArguments")}
            </TabsTrigger>
            <TabsTrigger value="result" className="pb-2 text-xs">
              {t("agentToolResult")}
            </TabsTrigger>
          </TabsList>
          <TabsContent value="arguments" className="pt-2">
            <ToolValue value={call.arguments} surface={surface} />
          </TabsContent>
          <TabsContent value="result" className="space-y-1 pt-2">
            {call.error !== null ? (
              <p className="text-xs text-destructive">{t("agentToolError")}</p>
            ) : null}
            <ToolValue value={call.error ?? call.result ?? statusLabel} surface={surface} />
          </TabsContent>
        </Tabs>
      </CollapsibleContent>
    </Collapsible>
  )
}

/** 按顺序展示已完成运行的文本、思考与工具调用内容块，调用成功的任务清单工具不逐条列出；thinkingClassName 是思考内容的文字颜色，inBubble 表示位于消息气泡内。 */
export function AgentRunBlocks({ blocks, thinkingClassName, inBubble, onToggle }: { blocks: AgentRunContentBlock[]; thinkingClassName: string; inBubble?: boolean; onToggle?: () => void }) {
  const { i18n } = useTranslation()
  return blocks.filter((block) => !isPlanToolBlock(block)).map((block) =>
    block.kind === AgentRunBlockKind.AgentRunBlockToolCall && block.toolCall ? (
      <AgentTool
        key={block.id}
        call={block.toolCall}
        detail={block.toolCall.name === delegateToolName ? delegateDescription(block.toolCall.arguments) || undefined : undefined}
        inBubble={inBubble}
        onToggle={onToggle}
      />
    ) : (
      <div
        key={block.id}
        className={cn("min-w-0 break-words", block.kind === AgentRunBlockKind.AgentRunBlockThinking && cn("italic", thinkingClassName))}
      >
        <MessageMarkdown locale={i18n.language} onOpenLink={openExternalURL}>{block.text}</MessageMarkdown>
      </div>
    ),
  )
}

/** 返回委派调用参数中的子任务说明，参数无法解析时返回空串。 */
function delegateDescription(argumentsJSON: string) {
  try {
    const value: unknown = JSON.parse(argumentsJSON)
    return typeof value === "object" && value !== null && "description" in value && typeof value.description === "string" ? value.description : ""
  } catch {
    return ""
  }
}
