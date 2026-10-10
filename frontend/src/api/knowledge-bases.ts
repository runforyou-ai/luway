/** 企业知识库调用。 */
import * as ops from "@/api/generated/operations"

/** 创建企业知识库。 */
export const createKnowledgeBase = ops.createKnowledgeBase

/** 读取企业知识库详情。 */
export const getKnowledgeBase = ops.getKnowledgeBase

/** 修改企业知识库。 */
export const updateKnowledgeBase = ops.updateKnowledgeBase

/** 删除企业知识库。 */
export const deleteKnowledgeBase = ops.deleteKnowledgeBase

/** 读取当前配置版本绑定知识库的 AI 员工。 */
export const listKnowledgeBaseAgents = ops.listKnowledgeBaseAgents

/** 读取当前企业的知识库列表。 */
export const listKnowledgeBases = ops.listKnowledgeBases

/** 读取指定知识库的问答列表。 */
export const listKnowledgeQAEntries = ops.listKnowledgeQAEntries

/** 读取完整问答。 */
export const getKnowledgeQAEntry = ops.getKnowledgeQAEntry

/** 创建本地问答。 */
export const createKnowledgeQAEntry = ops.createKnowledgeQAEntry

/** 修改本地问答。 */
export const updateKnowledgeQAEntry = ops.updateKnowledgeQAEntry

/** 删除完整问答。 */
export const deleteKnowledgeQAEntry = ops.deleteKnowledgeQAEntry

/** 按当前配置重新索引问答。 */
export const retryKnowledgeQAEntry = ops.retryKnowledgeQAEntry

/** 创建在线编写的文档。 */
export const createKnowledgeTextDocument = ops.createKnowledgeTextDocument

/** 导入网页作为知识文档。 */
export const createKnowledgeWebDocument = ops.createKnowledgeWebDocument

/** 重新抓取网页文档。 */
export const refetchKnowledgeDocument = ops.refetchKnowledgeDocument

/** 读取在线文档正文或网页抓取快照。 */
export const getKnowledgeDocumentContent = ops.getKnowledgeDocumentContent

/** 保存在线文档的名称与正文。 */
export const updateKnowledgeDocumentContent = ops.updateKnowledgeDocumentContent

/** 读取知识库文档列表。 */
export const listKnowledgeDocuments = ops.listKnowledgeDocuments

/** 读取文档详情。 */
export const getKnowledgeDocument = ops.getKnowledgeDocument

/** 将上传原件保存为文档。 */
export const createKnowledgeDocuments = ops.createKnowledgeDocuments

/** 删除文档并释放原件。 */
export const deleteKnowledgeDocument = ops.deleteKnowledgeDocument

/** 使用服务端签发的请求直接读取原件。 */
export async function readKnowledgeDocumentPreview(
  baseId: string,
  documentId: string,
  signal?: AbortSignal,
): Promise<Uint8Array<ArrayBuffer>> {
  const request = await ops.getKnowledgeDocumentPreview(baseId, documentId, signal)
  const headers = new Headers()
  for (const [name, value] of Object.entries(request.headers ?? {})) if (value !== undefined) headers.set(name, value)
  const response = await fetch(request.url, { headers, signal, cache: "no-store" })
  if (!response.ok) throw new Error(`Document preview failed: ${response.status}`)
  return new Uint8Array(await response.arrayBuffer())
}

/** 按当前配置重新处理文档。 */
export const retryKnowledgeDocument = ops.retryKnowledgeDocument

/** 读取固定批次的一页分段或锚点所在页。 */
export const listKnowledgeDocumentSegments = ops.listKnowledgeDocumentSegments

/** 在指定知识库中执行检索测试。 */
export const retrieveKnowledgeBase = ops.retrieveKnowledgeBase
