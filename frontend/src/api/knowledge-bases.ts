/** 企业知识库调用。 */
import {
  RetryKnowledgeDocument,
  CreateKnowledgeTextDocument,
  CreateKnowledgeWebDocument,
  RefetchKnowledgeDocument,
  GetKnowledgeDocumentContent,
  UpdateKnowledgeDocumentContent,
  RetrieveKnowledgeBase,
  ListKnowledgeDocumentSegments,
  ListKnowledgeDocuments,
  GetKnowledgeDocument,
  CreateKnowledgeDocuments,
  DeleteKnowledgeDocument,
  GetKnowledgeDocumentPreview,
  CreateKnowledgeQAEntry,
  UpdateKnowledgeQAEntry,
  GetKnowledgeQAEntry,
  ListKnowledgeQAEntries,
  DeleteKnowledgeQAEntry,
  RetryKnowledgeQAEntry,
  CreateKnowledgeBase,
  DeleteKnowledgeBase,
  GetKnowledgeBase,
  ListKnowledgeBaseAgents,
  ListKnowledgeBases,
  UpdateKnowledgeBase,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  KnowledgeIndexStatus,
  KnowledgeDocumentSourceKind,
  type KnowledgeDocumentList,
  type KnowledgeDocumentListInput,
  type KnowledgeDocument,
  type KnowledgeDocumentBatch,
  type KnowledgeDocumentBatchInput,
  type KnowledgeTextDocumentInput,
  type KnowledgeWebDocumentInput,
  type KnowledgeDocumentContent,
  type KnowledgeDocumentContentInput,
  type KnowledgeQAEntry,
  type KnowledgeQAList,
  type KnowledgeQAListInput,
  type KnowledgeQASummary,
  KnowledgeBaseCategory,
  type KnowledgeBase,
  type KnowledgeBaseAgentList,
  type KnowledgeBaseInput,
  type KnowledgeBaseList,
  type KnowledgeRetrievalResult,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type KnowledgeBaseCategoryId = Exclude<
  KnowledgeBaseCategory,
  KnowledgeBaseCategory.$zero
>

export type KnowledgeBaseData = Omit<NonNullArrays<KnowledgeBase>, "category"> & {
  category: KnowledgeBaseCategoryId
}

export type KnowledgeBaseAgentListData = NonNullArrays<KnowledgeBaseAgentList>

type KnowledgeBaseListData = Omit<
  NonNullArrays<KnowledgeBaseList>,
  "knowledgeBases"
> & {
  knowledgeBases: KnowledgeBaseData[]
}

const createKnowledgeBaseBound = bind(CreateKnowledgeBase)
const getKnowledgeBaseBound = bind(GetKnowledgeBase)
const updateKnowledgeBaseBound = bind(UpdateKnowledgeBase)
const listKnowledgeBasesBound = bind(ListKnowledgeBases)
/** 创建企业知识库。 */
export function createKnowledgeBase(input: KnowledgeBaseInput) {
  return createKnowledgeBaseBound(input) as Promise<KnowledgeBaseData>
}

/** 读取企业知识库详情。 */
export function getKnowledgeBase(knowledgeBaseId: string, signal?: AbortSignal) {
  return getKnowledgeBaseBound(
    knowledgeBaseId,
    signal,
  ) as Promise<KnowledgeBaseData>
}

/** 修改企业知识库。 */
export function updateKnowledgeBase(
  knowledgeBaseId: string,
  input: KnowledgeBaseInput,
) {
  return updateKnowledgeBaseBound(
    knowledgeBaseId,
    input,
  ) as Promise<KnowledgeBaseData>
}

/** 删除企业知识库。 */
export const deleteKnowledgeBase = bind(DeleteKnowledgeBase)

/** 读取当前配置版本绑定知识库的 AI 员工。 */
export const listKnowledgeBaseAgents = bind(ListKnowledgeBaseAgents)

/** 读取当前企业的知识库列表。 */
export function listKnowledgeBases() {
  return listKnowledgeBasesBound() as Promise<KnowledgeBaseListData>
}

export type KnowledgeIndexStatusId = Exclude<
  KnowledgeIndexStatus,
  KnowledgeIndexStatus.$zero
>

export type KnowledgeQAEntryData = NonNullArrays<KnowledgeQAEntry>

export type KnowledgeQASummaryData = Omit<
  NonNullArrays<KnowledgeQASummary>,
  "status"
> & {
  status: KnowledgeIndexStatusId
}

type KnowledgeQAListData = Omit<
  NonNullArrays<KnowledgeQAList>,
  "entries"
> & {
  entries: KnowledgeQASummaryData[]
}

const listKnowledgeQAEntriesBound = bind(ListKnowledgeQAEntries)

/** 读取指定知识库的问答列表。 */
export function listKnowledgeQAEntries(
  knowledgeBaseId: string,
  input: KnowledgeQAListInput,
  signal?: AbortSignal,
) {
  return listKnowledgeQAEntriesBound(
    knowledgeBaseId,
    input,
    signal,
  ) as Promise<KnowledgeQAListData>
}

/** 读取完整问答。 */
export const getKnowledgeQAEntry = bind(GetKnowledgeQAEntry)

/** 创建本地问答。 */
export const createKnowledgeQAEntry = bind(CreateKnowledgeQAEntry)

/** 修改本地问答。 */
export const updateKnowledgeQAEntry = bind(UpdateKnowledgeQAEntry)

/** 删除完整问答。 */
export const deleteKnowledgeQAEntry = bind(DeleteKnowledgeQAEntry)

/** 按当前配置重新索引问答。 */
export const retryKnowledgeQAEntry = bind(RetryKnowledgeQAEntry)

type KnowledgeDocumentSourceKindId = Exclude<
  KnowledgeDocumentSourceKind,
  KnowledgeDocumentSourceKind.$zero
>
export type KnowledgeDocumentData = Omit<
  NonNullArrays<KnowledgeDocument>,
  "status" | "sourceKind"
> & {
  status: KnowledgeIndexStatusId
  sourceKind: KnowledgeDocumentSourceKindId
}
type KnowledgeDocumentListData = Omit<
  NonNullArrays<KnowledgeDocumentList>,
  "documents"
> & {
  documents: KnowledgeDocumentData[]
}
type KnowledgeDocumentBatchData = Omit<
  NonNullArrays<KnowledgeDocumentBatch>,
  "documents"
> & {
  documents: KnowledgeDocumentData[]
}
export type KnowledgeDocumentContentData = Omit<NonNullArrays<KnowledgeDocumentContent>, "document"> & {
  document: KnowledgeDocumentData
}
const createKnowledgeTextDocumentBound = bind(CreateKnowledgeTextDocument)
/** 创建在线编写的文档。 */
export function createKnowledgeTextDocument(
  baseId: string,
  input: KnowledgeTextDocumentInput,
) {
  return createKnowledgeTextDocumentBound(
    baseId,
    input,
  ) as Promise<KnowledgeDocumentData>
}
const createKnowledgeWebDocumentBound = bind(CreateKnowledgeWebDocument)
/** 导入网页作为知识文档。 */
export function createKnowledgeWebDocument(
  baseId: string,
  input: KnowledgeWebDocumentInput,
) {
  return createKnowledgeWebDocumentBound(
    baseId,
    input,
  ) as Promise<KnowledgeDocumentData>
}
/** 重新抓取网页文档。 */
export const refetchKnowledgeDocument = bind(RefetchKnowledgeDocument)
const getKnowledgeDocumentContentBound = bind(GetKnowledgeDocumentContent)
/** 读取在线文档正文或网页抓取快照。 */
export function getKnowledgeDocumentContent(
  baseId: string,
  documentId: string,
  signal?: AbortSignal,
) {
  return getKnowledgeDocumentContentBound(
    baseId,
    documentId,
    signal,
  ) as Promise<KnowledgeDocumentContentData>
}
const updateKnowledgeDocumentContentBound = bind(UpdateKnowledgeDocumentContent)
/** 保存在线文档的名称与正文。 */
export function updateKnowledgeDocumentContent(
  baseId: string,
  documentId: string,
  input: KnowledgeDocumentContentInput,
) {
  return updateKnowledgeDocumentContentBound(
    baseId,
    documentId,
    input,
  ) as Promise<KnowledgeDocumentData>
}
const listKnowledgeDocumentsBound = bind(ListKnowledgeDocuments)
const createKnowledgeDocumentsBound = bind(CreateKnowledgeDocuments)
/** 读取知识库文档列表。 */
export function listKnowledgeDocuments(
  baseId: string,
  input: KnowledgeDocumentListInput,
  signal?: AbortSignal,
) {
  return listKnowledgeDocumentsBound(
    baseId,
    input,
    signal,
  ) as Promise<KnowledgeDocumentListData>
}
const getKnowledgeDocumentBound = bind(GetKnowledgeDocument)
/** 读取文档详情。 */
export function getKnowledgeDocument(
  baseId: string,
  documentId: string,
  signal?: AbortSignal,
) {
  return getKnowledgeDocumentBound(
    baseId,
    documentId,
    signal,
  ) as Promise<KnowledgeDocumentData>
}
/** 将上传原件保存为文档。 */
export function createKnowledgeDocuments(
  baseId: string,
  input: KnowledgeDocumentBatchInput,
) {
  return createKnowledgeDocumentsBound(
    baseId,
    input,
  ) as Promise<KnowledgeDocumentBatchData>
}
/** 删除文档并释放原件。 */
export const deleteKnowledgeDocument = bind(DeleteKnowledgeDocument)
/** 取得用于预览的原件读取请求。 */
const getKnowledgeDocumentPreview = bind(GetKnowledgeDocumentPreview)

/** 使用服务端签发的请求直接读取原件。 */
export async function readKnowledgeDocumentPreview(
  baseId: string,
  documentId: string,
  signal?: AbortSignal,
): Promise<Uint8Array<ArrayBuffer>> {
  const request = await getKnowledgeDocumentPreview(baseId, documentId, signal)
  const headers = new Headers()
  for (const [name, value] of Object.entries(request.headers ?? {})) if (value !== undefined) headers.set(name, value)
  const response = await fetch(request.url, { headers, signal, cache: "no-store" })
  if (!response.ok) throw new Error(`Document preview failed: ${response.status}`)
  return new Uint8Array(await response.arrayBuffer())
}

/** 按当前配置重新处理文档。 */
export const retryKnowledgeDocument = bind(RetryKnowledgeDocument)

/** 读取固定批次的一页分段或锚点所在页。 */
export const listKnowledgeDocumentSegments = bind(ListKnowledgeDocumentSegments)

export type KnowledgeRetrievalResultData = NonNullArrays<KnowledgeRetrievalResult>

/** 在指定知识库中执行检索测试。 */
export const retrieveKnowledgeBase = bind(RetrieveKnowledgeBase)
