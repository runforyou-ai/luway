/** 知识库列表页：新建、进入内容管理、编辑与删除知识库。 */
import { CircleHelpIcon, FileTextIcon, PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate } from "react-router"
import { toast } from "sonner"

import {
  deleteKnowledgeBase,
  isApiError,
  KnowledgeBaseCategory,
  listKnowledgeBaseAgents,
  listKnowledgeBases,
  sessionPath,
  UserStatus,
  type KnowledgeBaseAgentList,
  type KnowledgeBase,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useDateTime } from "@/hooks/use-date-time"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { useResource, useResourceReader } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"

type DeleteKnowledgeBaseTarget = {
  knowledgeBase: KnowledgeBase
  agents: KnowledgeBaseAgentList["agents"]
}

/** 列出知识库，删除前读取正在使用它的 AI 员工。 */
export function KnowledgeBaseListPage() {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const readResource = useResourceReader()
  const mounted = useMountedRef()
  const resource = useResource(resourceKeys.knowledgeBases(), () => listKnowledgeBases())
  const knowledgeBases = resource.data?.knowledgeBases ?? []

  const deletion = useConfirmedAction<DeleteKnowledgeBaseTarget>({
    action: (target) => deleteKnowledgeBase(target.knowledgeBase.id),
    invalidateKeys: () => [resourceKeys.knowledgeBases()],
    logLabel: "知识库删除",
    successMessage: () => t("delete.success"),
    errorMessage: () => t("delete.error"),
  })
  const deletingAgents = deletion.item?.agents ?? []

  /** 读取最新的 AI 员工绑定后打开知识库删除确认。 */
  async function requestDelete(knowledgeBase: KnowledgeBase) {
    try {
      const result = await readResource(
        resourceKeys.knowledgeBaseAgents(knowledgeBase.id),
        () => listKnowledgeBaseAgents(knowledgeBase.id),
      )
      if (!mounted.current) return
      deletion.select({ knowledgeBase, agents: result.agents })
    } catch (error) {
      if (!mounted.current) return
      if (isApiError(error) && sessionPath(error.state)) return
      console.warn("知识库 AI 员工读取失败", {
        knowledge_base_id: knowledgeBase.id,
        error,
      })
      toast.error(
        isApiError(error) ? apiErrorMessage(error) : t("agents.loadError"),
      )
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("title")} description={t("description")}>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="subtle"
              size="icon-sm"
              aria-label={t("list.create")}
              title={t("list.create")}
            >
              <PlusIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-48">
            <DropdownMenuItem asChild>
              <Link to="/knowledge-bases/new?category=standard">
                <FileTextIcon />
                {t("list.createStandard")}
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem asChild>
              <Link to="/knowledge-bases/new?category=qa">
                <CircleHelpIcon />
                {t("list.createQA")}
              </Link>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </PageHeader>

      <ResourceListLayout resources={resource} errorMessage={t("list.loadError")}>
        <ResourceTable
          columns={[
            {
              key: "name",
              header: t("list.columns.name"),
              cellClassName: "min-w-0",
              cell: (knowledgeBase) => {
                const isQA =
                  knowledgeBase.category ===
                  KnowledgeBaseCategory.QA
                return (
                  <ResourceRowIdentity
                    icon={isQA ? CircleHelpIcon : FileTextIcon}
                    name={knowledgeBase.name}
                    secondary={t(isQA ? "category.qaShort" : "category.standardShort")}
                    description={knowledgeBase.description || undefined}
                  />
                )
              },
            },
            {
              key: "time",
              header: t("list.columns.createdAt"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (knowledgeBase) =>
                t("list.createdAt", { time: formatDateTime(knowledgeBase.createdAt) }),
            },
          ]}
          rows={knowledgeBases}
          rowKey={(knowledgeBase) => knowledgeBase.id}
          empty={t("list.empty")}
          onRowActivate={(knowledgeBase) =>
            navigate(
              `/knowledge-bases/${knowledgeBase.id}/${knowledgeBase.category === KnowledgeBaseCategory.QA ? "qa" : "documents"}`,
            )
          }
          rowActions={(knowledgeBase) => [
            {
              key: "edit",
              label: t("common:actions.edit"),
              onSelect: () => navigate(`/knowledge-bases/${knowledgeBase.id}`),
            },
            {
              key: "delete",
              label: t("common:actions.delete"),
              destructive: true,
              separatorBefore: true,
              onSelect: () => void requestDelete(knowledgeBase),
            },
          ]}
        />
      </ResourceListLayout>

      <ConfirmationDialog
        {...deletion.dialog}
        title={
          deletion.item
            ? t("delete.title", { name: deletion.item.knowledgeBase.name })
            : ""
        }
        description={
          deletingAgents.length > 0
            ? t("delete.agentsDescription", {
                count: deletingAgents.length,
                names: deletingAgents
                  .map((agent) =>
                    agent.status === UserStatus.Active
                      ? agent.displayName
                      : t("agents.inactive", { name: agent.displayName }),
                  )
                  .join(t("delete.agentSeparator")),
              })
            : t("delete.description")
        }
        pendingLabel={t("common:actions.deleting")}
      />
    </div>
  )
}
