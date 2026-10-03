/** AI 员工独立创建页和详情页：创建页先选择服务对象，仅自己时切换为本机创建表单；详情页分概览、待补知识、问题会话、评测、服务记录、基本资料与运行配置七个与地址同步的页签，没有服务对象的 AI 员工不显示评测。 */
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { useParams, useSearchParams } from "react-router"

import {
  currentComputer,
  getAIPerformanceReport,
  getAgent,
  isNotFoundApiError,
  listComputers,
  listTeams,
  type AgentData,
  type ServiceAudience,
} from "@/api"
import { ListToolbar, ListToolbarFilter } from "@/components/list-toolbar"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ReportPeriodFilter } from "@/components/report-parts"
import { ResourceContent } from "@/components/resource-content"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { AgentForm, useAgentAvatarUpload, type AgentCreateDraft } from "@/features/agents/agent-form"
import { PersonalAgentCreateForm } from "@/features/agents/personal/personal-agent-form"
import { AgentProfileForm } from "@/features/agents/agent-profile-form"
import { AgentExecutionForm } from "@/features/agents/agent-execution-form"
import { AgentEvaluationPanel } from "@/features/agents/agent-evaluation"
import { AgentServiceRecords } from "@/features/agents/agent-service-records"
import { aiIssueTypes, gapStatuses } from "@/features/agents/ai-performance-format"
import { AIKnowledgeGapList, AIPerformanceIssueList } from "@/features/agents/ai-performance-lists"
import { useReportSearchParams } from "@/features/agents/report-filters"
import { AIPerformanceOverview } from "@/features/agents/ai-performance-overview"
import { useContactInvalidator } from "@/hooks/use-contact-invalidator"
import { resourceKeys } from "@/hooks/resource-keys"
import { periodOptions } from "@/hooks/use-report-format"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { useReturnTo } from "@/hooks/use-return-to"
import { cn } from "@/lib/utils"

/** 详情页签，第一个为默认值。 */
const detailTabs = ["overview", "knowledgeGaps", "issues", "evaluation", "records", "basic", "execution"] as const

type DetailTab = (typeof detailTabs)[number]

/** 详情页地址参数的默认值，等于默认值时从地址中移除。 */
const parameterDefaults = {
  tab: detailTabs[0],
  days: String(periodOptions[0]),
  status: gapStatuses[0],
  gap: "",
  issue: aiIssueTypes[0],
  session: "",
  case: "",
}

/** 打开条目的地址参数。 */
const openItems = ["gap", "session", "case"] as const

/** 加载 AI 员工详情；新建时只显示创建表单。 */
export function AgentFormPage({ mode }: { mode: "create" | "edit" }) {
  const { t } = useTranslation(["agents", "common"])
  const { agentId = "" } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const detail = useResource(
    resourceKeys.agent(agentId),
    () => getAgent(agentId),
    { enabled: mode === "edit" },
  )
  const agent = detail.data
  const tab = detailTabs.find((value) => value === searchParams.get("tab")) ?? detailTabs[0]
  // 返回来源 AI 员工列表或团队页。
  const { returnTo, leave } = useReturnTo("/ai-employees", {
    allowed: (path) => /^\/contacts\/teams\/[^/]+$/.test(path),
    notFound: mode === "edit" && isNotFoundApiError(detail.error),
    logFields: { agent_id: agentId },
  })
  const teamId = searchParams.get("teamId")

  // 无效页签改写为有效页签，缺省时表示默认页签。
  useEffect(() => {
    const current = searchParams.get("tab")
    if (mode !== "edit" || current === null || current === tab) return
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next, { replace: true })
  }, [mode, searchParams, setSearchParams, tab])

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={mode === "create" ? t("create") : (agent?.displayName ?? t("editTitle"))}
        description={t(mode === "create" ? "createDescription" : "editDescription")}
        backTo={mode === "edit" ? returnTo : undefined}
      />
      {mode === "create" ? (
        <PageContent variant="form">
          <AgentCreateForms
            defaultTeamIds={teamId ? [teamId] : []}
            onCancel={() => leave()}
            onSaved={() => leave({ replace: true })}
          />
        </PageContent>
      ) : (
        <ResourceContent resources={detail} errorMessage={t("form.loadError")}>
          {agent ? <AgentDetailTabs key={agent.id} agent={agent} tab={tab} /> : null}
        </ResourceContent>
      )}
    </div>
  )
}

/** 按服务对象切换创建表单：客户或员工使用 AI 员工表单，仅自己使用本机创建表单，切换时保留已填写的名称与托管执行配置；本机未注册为电脑时不可选择仅自己。 */
function AgentCreateForms({
  defaultTeamIds,
  onSaved,
  onCancel,
}: {
  defaultTeamIds: string[]
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation("agents")
  const [personal, setPersonal] = useState(false)
  const [serviceAudiences, setServiceAudiences] = useState<ServiceAudience[]>([])
  const [draft, setDraft] = useState<AgentCreateDraft>({ displayName: "", modelId: "", systemInstruction: "", knowledgeBaseIds: [] })
  // 头像上传与已带入的草稿跨表单保留，切换后仍按未保存内容拦截离开。
  const avatar = useAgentAvatarUpload()
  const draftDirty =
    draft.displayName !== "" || draft.modelId !== "" || draft.systemInstruction !== "" || draft.knowledgeBaseIds.length > 0
  const local = useResource(resourceKeys.currentComputer(), () => currentComputer())
  const computers = useResource(resourceKeys.computers(), () => listComputers(), { enabled: personal })
  const localComputerID = local.data?.computerId ?? ""
  const localComputer = computers.data?.computers.find((computer) => computer.id === localComputerID)

  if (personal && localComputerID) {
    return (
      <ResourceContent resources={computers} errorMessage={t("personal.loadError")}>
        <PersonalAgentCreateForm
          computerID={localComputerID}
          computerName={localComputer?.name ?? ""}
          draft={draft}
          draftDirty={draftDirty}
          avatar={avatar}
          onServiceAudiencesChange={(audiences, next) => {
            setServiceAudiences(audiences)
            setDraft(next)
            setPersonal(false)
          }}
          onCancel={onCancel}
          onSaved={onSaved}
        />
      </ResourceContent>
    )
  }
  return (
    <AgentForm
      defaultTeamIds={defaultTeamIds}
      defaultServiceAudiences={serviceAudiences}
      draft={draft}
      draftDirty={draftDirty}
      avatar={avatar}
      personal={{
        available: localComputerID !== "",
        onSelect: (next) => {
          setDraft(next)
          setPersonal(true)
        },
      }}
      onCancel={onCancel}
      onSaved={onSaved}
    />
  )
}

/** AI 员工详情的页签与各页签内容；页签、统计天数、待补知识状态、问题类型与打开的条目保存在地址中，两个配置表单保持挂载以保留未保存的修改，团队只在基本资料中读取。 */
function AgentDetailTabs({ agent, tab: requestedTab }: { agent: AgentData; tab: DetailTab }) {
  const { t } = useTranslation("agents")
  const [searchParams, setParameters] = useReportSearchParams(parameterDefaults, openItems)
  const [, setSearchParams] = useSearchParams()
  const invalidateContact = useContactInvalidator()
  const invalidate = useResourceInvalidator()
  const teams = useResource(resourceKeys.teams({ pageSize: 100 }), () => listTeams({ pageSize: 100 }))
  const days =
    periodOptions.find((option) => String(option) === searchParams.get("days")) ??
    periodOptions[0]
  const status = gapStatuses.find((value) => value === searchParams.get("status")) ?? gapStatuses[0]
  const gapId = searchParams.get("gap") ?? ""
  const issue = aiIssueTypes.find((value) => value === searchParams.get("issue")) ?? aiIssueTypes[0]
  const sessionId = searchParams.get("session") ?? ""
  const caseId = searchParams.get("case") ?? ""
  const tabs = detailTabs.filter((value) => value !== "evaluation" || agent.serviceAudiences.length > 0)
  const tab = tabs.includes(requestedTab) ? requestedTab : tabs[0]

  // 地址中的页签对该 AI 员工不可显示时改为第一个页签。
  useEffect(() => {
    if (tab === requestedTab) return
    setSearchParams(
      (current) => {
        const next = new URLSearchParams(current)
        next.set("tab", tab)
        return next
      },
      { replace: true },
    )
  }, [requestedTab, setSearchParams, tab])
  const filter = { channelId: "", agentId: agent.id, mine: false }
  const report = useResource(
    resourceKeys.aiPerformanceReport({ days, ...filter }),
    () => getAIPerformanceReport({ days, ...filter }),
    { keepPreviousData: true, enabled: tab === "overview" },
  )

  return (
    <>
      <div className="app-page-gutter shrink-0">
        <Tabs value={tab} onValueChange={(value) => setParameters({ tab: value as DetailTab })}>
          <TabsList>
            {tabs.map((value) => (
              <TabsTrigger key={value} value={value}>
                {t(`detailTabs.${value}`)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>
      {tab === "overview" ? (
        <>
          <ListToolbar>
            <ReportPeriodFilter value={days} onValueChange={(value) => setParameters({ days: value })} />
          </ListToolbar>
          <PageContent>
            <ResourceContent resources={report} errorMessage={t("performance.loadError")}>
              {report.data ? (
                <AIPerformanceOverview
                  report={report.data}
                  onOpenKnowledgeGaps={() => setParameters({ tab: "knowledgeGaps", status: gapStatuses[0] })}
                  onOpenIssues={(value) => setParameters({ tab: "issues", issue: value })}
                />
              ) : null}
            </ResourceContent>
          </PageContent>
        </>
      ) : null}
      {tab === "knowledgeGaps" ? (
        <>
          <ListToolbar>
            <ListToolbarFilter
              label={t("performance.gapStatus")}
              value={status}
              options={gapStatuses.map((value) => ({
                value,
                label: t(`performance.gapStatuses.${value}`),
              }))}
              onValueChange={(value) => setParameters({ status: value })}
            />
          </ListToolbar>
          <AIKnowledgeGapList
            filter={filter}
            status={status}
            gapId={gapId}
            onGapChange={(value) => setParameters({ gap: value })}
          />
        </>
      ) : null}
      {tab === "issues" ? (
        <>
          <ListToolbar>
            <ReportPeriodFilter value={days} onValueChange={(value) => setParameters({ days: value })} />
            <ListToolbarFilter
              label={t("performance.issueType")}
              value={issue}
              options={aiIssueTypes.map((value) => ({
                value,
                label: t(`performance.issueTypes.${value}`),
              }))}
              onValueChange={(value) => setParameters({ issue: value })}
            />
          </ListToolbar>
          <AIPerformanceIssueList
            days={days}
            filter={filter}
            issue={issue}
            serviceSessionId={sessionId}
            onIssueOpen={(value) => setParameters({ session: value })}
          />
        </>
      ) : null}
      {tab === "evaluation" ? (
        <AgentEvaluationPanel agent={agent} caseId={caseId} onCaseChange={(value) => setParameters({ case: value })} />
      ) : null}
      {tab === "records" ? <AgentServiceRecords agentId={agent.id} /> : null}
      <PageContent
        variant="form"
        className={cn(tab !== "basic" && tab !== "execution" && "hidden")}
      >
        <div className={cn(tab !== "basic" && "hidden")}>
          <ResourceContent resources={teams} errorMessage={t("form.loadError")}>
            <AgentProfileForm
              agent={agent}
              teams={teams.data?.teams ?? []}
              onSaved={() => {
                void invalidateContact("agent", agent.id)
                // 负责人变化影响「我负责的」范围。
                void invalidate(resourceKeys.knowledgeGaps())
                void invalidate(resourceKeys.aiPerformanceReport())
                void invalidate(resourceKeys.aiPerformanceBreakdowns())
                void invalidate(resourceKeys.aiPerformanceIssues())
              }}
            />
          </ResourceContent>
        </div>
        <div className={cn(tab !== "execution" && "hidden")}>
          <AgentExecutionForm
            agent={agent}
            onSaved={() => {
              void invalidateContact("agent", agent.id)
            }}
          />
        </div>
      </PageContent>
    </>
  )
}
