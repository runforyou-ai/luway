/** 业务系统编辑页的工具页签：工具目录、操作级别与工具设置侧栏。 */
import { useState } from "react"
import { WrenchIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import type { BusinessSystem } from "@/api"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { BusinessToolSheet } from "@/features/integrations/business-systems/business-tool-sheet"

/** 按目录顺序列出工具及 HTTP 工具对应的接口，行内显示操作级别、对外发信与停用状态，点击行打开工具设置。 */
export function BusinessSystemTools({ system }: { system: BusinessSystem }) {
  const { t } = useTranslation("integrations")
  const [selectedName, setSelectedName] = useState<string | null>(null)
  const selected = system.tools.find((tool) => tool.name === selectedName) ?? null
  const status = system.toolsUpdating
    ? t("businessSystem.tools.updating")
    : system.toolsError
      ? t("businessSystem.tools.failed", { message: system.toolsError })
      : null

  return (
    <div className="space-y-3">
      {status ? (
        <p className={system.toolsError ? "text-sm text-destructive" : "text-sm text-muted-foreground"}>{status}</p>
      ) : null}
      <ResourceTable
        columns={[
          {
            key: "tool",
            header: t("businessSystem.tabs.tools"),
            cellClassName: "min-w-0",
            cell: (tool) => (
              <ResourceRowIdentity
                icon={WrenchIcon}
                name={<span className="font-mono">{tool.name}</span>}
                // HTTP 工具名称不是「方法 路径」时另行显示对应的接口。
                secondary={tool.http && tool.name !== `${tool.http.method} ${tool.http.path}` ? (
                  <span className="font-mono">{tool.http.method} {tool.http.path}</span>
                ) : undefined}
                description={tool.description || t("businessSystem.tools.noDescription")}
              />
            ),
          },
          {
            key: "level",
            header: t("businessSystem.toolSetting.level"),
            className: "w-px",
            cell: (tool) => (
              <div className="flex items-center justify-end gap-1.5">
                {tool.disabled ? <StatusBadge variant="muted">{t("businessSystem.tools.disabled")}</StatusBadge> : null}
                {tool.facts.outbound ? <StatusBadge variant="muted">{t("businessSystem.tools.outbound")}</StatusBadge> : null}
                <StatusBadge variant="muted">{t(`businessSystem.levels.${tool.level}`)}</StatusBadge>
              </div>
            ),
          },
        ]}
        rows={system.tools}
        rowKey={(tool) => tool.name}
        empty={system.toolsUpdatedAt ? t("businessSystem.tools.empty") : t("businessSystem.tools.pending")}
        onRowActivate={(tool) => setSelectedName(tool.name)}
      />
      <BusinessToolSheet systemId={system.id} tool={selected} onClose={() => setSelectedName(null)} />
    </div>
  )
}
