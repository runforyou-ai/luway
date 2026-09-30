/** MCP 服务编辑页的工具用途标记。 */
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  MCPToolPurpose,
  isApiError,
  updateMCPToolPurpose,
  type MCPServerData,
} from "@/api"
import {
  Field,
  FieldDescription,
  FieldLabel,
} from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 逐个工具选择查询或操作用途，选择后立即保存。 */
export function MCPServerToolPurposes({ server }: { server: MCPServerData }) {
  const { t } = useTranslation("integrations")
  const navigate = useNavigate()
  const invalidateResource = useResourceInvalidator()
  const [saving, setSaving] = useState<string | null>(null)
  // 保存与重读详情期间显示本次选择的用途。
  const [selected, setSelected] = useState<Record<string, MCPToolPurpose>>({})

  /** 保存一个工具的用途并重读服务详情与列表，结束后以服务端的用途为准。 */
  async function save(toolName: string, purpose: MCPToolPurpose) {
    setSaving(toolName)
    setSelected((current) => ({ ...current, [toolName]: purpose }))
    try {
      await updateMCPToolPurpose(server.id, { toolName, purpose })
      void invalidateResource(resourceKeys.mcpServers())
      await invalidateResource(resourceKeys.mcpServer(server.id))
    } catch (error) {
      if (recoverSession(error, navigate)) return
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("mcpServer.purposes.saveError"))
    } finally {
      setSaving(null)
      setSelected(({ [toolName]: _saved, ...rest }) => rest)
    }
  }

  return (
    <Field>
      <FieldLabel>{t("mcpServer.purposes.label")}</FieldLabel>
      <FieldDescription>{t("mcpServer.purposes.help")}</FieldDescription>
      {server.tools.length ? (
        <ul className="divide-y rounded-lg border">
          {server.tools.map((tool) => (
            <li key={tool.name} className="flex items-center gap-4 px-4 py-3">
              <div className="min-w-0 flex-1">
                <p className="truncate font-mono text-sm font-medium" title={tool.name}>
                  {tool.name}
                </p>
                <p className="line-clamp-2 text-xs text-muted-foreground" title={tool.description}>
                  {tool.description || t("mcpServer.tools.noDescription")}
                </p>
              </div>
              <NativeSelect
                aria-label={t("mcpServer.purposes.selectLabel", { name: tool.name })}
                className="w-28 shrink-0"
                value={selected[tool.name] ?? tool.purpose}
                disabled={saving === tool.name}
                onChange={(event) => void save(tool.name, event.target.value as MCPToolPurpose)}
              >
                <option value={MCPToolPurpose.MCPToolPurposeUnmarked}>
                  {t("mcpServer.purposes.unmarked")}
                </option>
                <option value={MCPToolPurpose.MCPToolPurposeQuery}>
                  {t("mcpServer.purposes.query")}
                </option>
                <option value={MCPToolPurpose.MCPToolPurposeAction}>
                  {t("mcpServer.purposes.action")}
                </option>
              </NativeSelect>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted-foreground">
          {server.toolsUpdatedAt ? t("mcpServer.tools.empty") : t("mcpServer.tools.pending")}
        </p>
      )}
    </Field>
  )
}
