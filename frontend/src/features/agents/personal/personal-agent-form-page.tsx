/** 个人 AI 员工编辑页，分为基本信息与记忆两个与地址同步的页签。 */
import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { useParams, useSearchParams } from "react-router"

import { getPersonalAgent, isNotFoundApiError } from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { PersonalAgentEditForm } from "@/features/agents/personal/personal-agent-form"
import { usePersonalAgentInvalidator } from "@/hooks/use-personal-agent-invalidator"
import { AgentMemoryPanel } from "@/features/agents/personal/agent-memory-panel"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { useReturnTo } from "@/hooks/use-return-to"

/** 编辑当前成员负责的个人 AI 员工。 */
export function PersonalAgentFormPage() {
  const { t } = useTranslation("agents")
  const { agentId = "" } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const invalidate = usePersonalAgentInvalidator()
  const detail = useResource(resourceKeys.personalAgent(agentId), () => getPersonalAgent(agentId))
  const tab = searchParams.get("tab") === "memory" ? "memory" : "basic"
  // 个人 AI 员工不存在或不属于本人时返回来源列表。
  const { returnTo } = useReturnTo("/ai-employees", {
    notFound: isNotFoundApiError(detail.error),
    logFields: { agent_id: agentId },
  })

  // 缺省或无效页签统一写回地址，刷新时恢复同一页签。
  useEffect(() => {
    if (searchParams.get("tab") === tab) return
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams, tab])

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={detail.data?.personalAgent.displayName ?? t("personal.editTitle")}
        description={t("personal.editDescription")}
        backTo={returnTo}
      />
      <PageContent variant="form">
        <ResourceContent resources={[detail]} errorMessage={t("personal.loadError")}>
          {detail.data ? (
            <Tabs
              key={detail.data.personalAgent.id}
              value={tab}
              onValueChange={(value) => {
                const next = new URLSearchParams(searchParams)
                next.set("tab", value)
                setSearchParams(next, { replace: true })
              }}
            >
              <TabsList>
                <TabsTrigger value="basic">{t("personal.tabs.basic")}</TabsTrigger>
                <TabsTrigger value="memory">{t("personal.tabs.memory")}</TabsTrigger>
              </TabsList>
              <TabsContent value="basic" forceMount className="mt-6 data-[state=inactive]:hidden">
                <PersonalAgentEditForm detail={detail.data} onSaved={() => void invalidate(agentId)} />
              </TabsContent>
              <TabsContent value="memory" className="mt-6">
                <AgentMemoryPanel agentId={detail.data.personalAgent.id} />
              </TabsContent>
            </Tabs>
          ) : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}
