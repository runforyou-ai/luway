/** MCP 工具数量与目录提示。 */
import { LoaderCircleIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import type { MCPServerData } from "@/api"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/** 以一行小字显示工具数量或更新状态，悬停展开工具目录。 */
export function MCPServerToolsCell({ server }: { server: MCPServerData }) {
  const { t } = useTranslation("integrations")
  const tools = server.tools
  const label = server.toolsUpdating
    ? t("mcpServer.tools.updating")
    : server.toolsError || (server.toolsUpdatedAt
      ? t("mcpServer.tools.count", { count: tools.length })
      : t("mcpServer.tools.pending"))

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          className={`inline-flex max-w-60 min-w-0 items-center gap-1 rounded-sm tabular-nums outline-none focus-visible:ring-2 focus-visible:ring-ring ${server.toolsError ? "text-destructive" : "text-muted-foreground"}`}
          // 行整体可点击进入编辑，查看工具目录时不触发跳转。
          onClick={(event) => event.stopPropagation()}
        >
          {server.toolsUpdating ? (
            <LoaderCircleIcon className="size-3 shrink-0 animate-spin" aria-hidden="true" />
          ) : null}
          <span className="truncate">{label}</span>
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom" align="start" sideOffset={4} className="max-w-sm text-left text-wrap">
        <div className="max-h-72 space-y-3 overflow-y-auto overscroll-contain">
          {server.toolsUpdating ? <p>{t("mcpServer.tools.updating")}</p> : null}
          {server.toolsError ? <p>{t("mcpServer.tools.failed", { message: server.toolsError })}</p> : null}
          {tools.length ? (
            <ul className="space-y-3">
              {tools.map((tool) => (
                <li key={tool.name} className="space-y-1 break-words">
                  <p className="font-mono font-medium">{tool.name}</p>
                  <p className="whitespace-pre-wrap opacity-80">{tool.description || t("mcpServer.tools.noDescription")}</p>
                </li>
              ))}
            </ul>
          ) : (
            <p>{server.toolsUpdatedAt ? t("mcpServer.tools.empty") : t("mcpServer.tools.pending")}</p>
          )}
        </div>
      </TooltipContent>
    </Tooltip>
  )
}
