/** 本地知识问答的列表、搜索和删除操作。 */
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useLocation } from "react-router"

import {
  KnowledgeBaseCategory,
  deleteKnowledgeQAEntry,
  listKnowledgeQAEntries,
  type KnowledgeQASummaryData,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { Button } from "@/components/ui/button"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { resourceKeys } from "@/hooks/resource-keys"
import { KnowledgeQATable } from "@/features/knowledge-base/knowledge-qa-table"
import {
  KnowledgeContentListShell,
  KnowledgeContentRoute,
  useKnowledgeContentList,
} from "@/features/knowledge-base/knowledge-content-list"

/** 按知识库切换列表实例，隔离删除对话框和滚动恢复状态。 */
export function KnowledgeQAListPage() {
  return (
    <KnowledgeContentRoute>
      {(ids) => <KnowledgeQAList {...ids} />}
    </KnowledgeContentRoute>
  )
}

/** 展示当前知识库中的问答，并保留返回时的列表位置。 */
function KnowledgeQAList({
  knowledgeBaseId,
}: {
  knowledgeBaseId: string
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const location = useLocation()
  const list = useKnowledgeContentList({
    knowledgeBaseId,
    section: "qa",
    listKey: (parameters) => resourceKeys.knowledgeQAEntries(knowledgeBaseId, parameters),
    load: (parameters, signal) => listKnowledgeQAEntries(knowledgeBaseId, parameters, signal),
    items: (data) => data.entries,
    processing: (entry) => entry.status === "queued" || entry.status === "running",
  })
  const deletion = useConfirmedAction<KnowledgeQASummaryData>({
    action: (entry) => deleteKnowledgeQAEntry(knowledgeBaseId, entry.id),
    invalidateKeys: (entry) => [
      resourceKeys.knowledgeQAEntries(knowledgeBaseId),
      resourceKeys.knowledgeQAEntry(knowledgeBaseId, entry.id),
    ],
    logLabel: "问答删除",
    successMessage: () => t("qa.deleteSuccess"),
    errorMessage: () => t("qa.deleteError"),
  })

  return (
    <>
      <KnowledgeContentListShell
        list={list}
        knowledgeBaseId={knowledgeBaseId}
        category={KnowledgeBaseCategory.KnowledgeBaseCategoryQA}
        fallbackTitle={t("qa.title")}
        description={t("qa.description")}
        searchLabel={t("qa.search")}
        errorMessage={t("qa.loadError")}
        actions={
          <Button variant="subtle" size="icon-sm" asChild>
            <Link
              to={`${list.listPath}/new${location.search}`}
              aria-label={t("qa.create")}
              title={t("qa.create")}
            >
              <PlusIcon />
            </Link>
          </Button>
        }
      >
        <KnowledgeQATable
          knowledgeBaseId={knowledgeBaseId}
          entries={list.list.data?.items ?? []}
          more={list.list.more}
          loading={list.list.isPlaceholderData || list.list.refreshing}
          listPath={list.listPath}
          search={location.search}
          filtered={list.query !== ""}
          onDelete={deletion.select}
        />
      </KnowledgeContentListShell>
      <ConfirmationDialog
        {...deletion.dialog}
        title={t("qa.deleteTitle")}
        // 问题原文可能含换行或长串字符，保留换行并允许断词。
        description={
          <span className="whitespace-pre-wrap break-words">
            {t("qa.deleteDescription", {
              question: deletion.item?.question ?? "",
            })}
          </span>
        }
        pendingLabel={t("common:actions.deleting")}
      />
    </>
  )
}
