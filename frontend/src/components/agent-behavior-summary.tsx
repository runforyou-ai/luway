/** 只读展示角色对 AI 员工的内置工作规则与可用工具。 */
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronDownIcon } from "lucide-react"

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { agentToolLabel } from "@/lib/agent-tool-labels"

/** 折叠展示内置规则正文，工具清单常驻显示。 */
export function AgentBehaviorSummary({
  behavior,
}: {
  behavior: { instruction: string; tools: string[] }
}) {
  const { t } = useTranslation("common")
  const [open, setOpen] = useState(false)
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="text-muted-foreground">{t("agentBehavior.tools")}</span>
        {behavior.tools.map((tool) => (
          <span key={tool} className="rounded-md border px-2 py-0.5">
            {agentToolLabel(tool, t)}
          </span>
        ))}
      </div>
      <Collapsible
        open={open}
        onOpenChange={setOpen}
        className="min-w-0 overflow-hidden rounded-lg border"
      >
        <CollapsibleTrigger className="group flex w-full min-w-0 cursor-pointer items-center gap-3 px-3 py-2.5 text-left text-sm transition-colors hover:bg-muted/50 focus-visible:outline focus-visible:-outline-offset-2 focus-visible:outline-ring">
          <span className="min-w-0 flex-1 font-medium">
            {open ? t("agentBehavior.hide") : t("agentBehavior.show")}
          </span>
          <ChevronDownIcon
            aria-hidden
            className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-180"
          />
        </CollapsibleTrigger>
        <CollapsibleContent>
          <pre className="border-t bg-muted/40 px-3 py-3 font-sans text-sm leading-6 whitespace-pre-wrap">
            {behavior.instruction}
          </pre>
        </CollapsibleContent>
      </Collapsible>
    </div>
  )
}
