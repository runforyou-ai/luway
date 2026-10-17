/** Shell 路由：AI 员工表单页注入表现页签组合。 */
import { AgentFormPage } from "@/features/agents/public"

import { AgentDetailPerformance } from "@/apps/shell/agent-detail-performance"

/** 创建或编辑 AI 员工，表现相关页签由 shell 装配。 */
export function AgentFormRoute({ mode }: { mode: "create" | "edit" }) {
  return (
    <AgentFormPage
      mode={mode}
      renderPerformance={(ctx) => <AgentDetailPerformance {...ctx} />}
    />
  )
}
