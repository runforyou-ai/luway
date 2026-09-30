/** 标准知识库文档列表、上传和原地管理。 */
import { useTranslation } from "react-i18next"
import { useLocation } from "react-router"
import { deleteKnowledgeDocument, KnowledgeBaseCategory, listKnowledgeDocuments, type KnowledgeDocumentData } from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { resourceKeys } from "@/hooks/resource-keys"
import { KnowledgeDocumentUpload } from "./knowledge-document-upload"
import { KnowledgeDocumentTable } from "./knowledge-document-table"
import {
  KnowledgeContentListShell,
  KnowledgeContentRoute,
  useKnowledgeContentList,
} from "./knowledge-content-list"

/** 按知识库隔离上传批次、弹窗和滚动恢复状态。 */
export function KnowledgeDocumentListPage() {
  return (
    <KnowledgeContentRoute>
      {(ids) => <KnowledgeDocumentList {...ids} />}
    </KnowledgeContentRoute>
  )
}

/** 显示创建时间倒序的文档，返回时恢复原列表位置。 */
function KnowledgeDocumentList({
  knowledgeBaseId,
}: {
  knowledgeBaseId: string
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const location = useLocation()
  const list = useKnowledgeContentList({
    knowledgeBaseId,
    section: "documents",
    listKey: (parameters) => resourceKeys.knowledgeDocuments(knowledgeBaseId, parameters),
    load: (parameters, signal) => listKnowledgeDocuments(knowledgeBaseId, parameters, signal),
    items: (data) => data.documents,
    processing: (document) => document.status === "queued" || document.status === "running",
  })
  const deletion = useConfirmedAction<KnowledgeDocumentData>({
    action: (document) => deleteKnowledgeDocument(knowledgeBaseId, document.id),
    invalidateKeys: (document) => [
      resourceKeys.knowledgeDocuments(knowledgeBaseId),
      resourceKeys.knowledgeDocument(knowledgeBaseId, document.id),
      resourceKeys.knowledgeDocumentContent(knowledgeBaseId, document.id),
      resourceKeys.knowledgeDocumentFile(knowledgeBaseId, document.id),
    ],
    logLabel: "文档删除",
    successMessage: () => t("documents.deleteSuccess"),
    errorMessage: () => t("documents.operationFailed"),
  })
  return (
    <>
      <KnowledgeContentListShell
        list={list}
        knowledgeBaseId={knowledgeBaseId}
        category={KnowledgeBaseCategory.KnowledgeBaseCategoryStandard}
        fallbackTitle={t("documents.title")}
        description={t("documents.description")}
        searchLabel={t("documents.search")}
        errorMessage={t("documents.loadError")}
        actions={<KnowledgeDocumentUpload baseId={knowledgeBaseId} />}
      >
        <KnowledgeDocumentTable
          knowledgeBaseId={knowledgeBaseId}
          documents={list.list.data?.items ?? []}
          more={list.list.more}
          listPath={list.listPath}
          search={location.search}
          filtered={Boolean(list.query)}
          refreshing={list.list.isPlaceholderData}
          onDelete={deletion.select}
        />
      </KnowledgeContentListShell>
      <ConfirmationDialog
        {...deletion.dialog}
        title={t("common:actions.delete")}
        description={t("documents.deleteDescription", { name: deletion.item?.name ?? "" })}
        pendingLabel={t("common:actions.deleting")}
      />
    </>
  )
}
