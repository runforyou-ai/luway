/** 知识库与文档表单校验规则。 */
import { z } from "zod"

export const knowledgeBaseNameMaxLength = 120
export const knowledgeBaseDescriptionMaxLength = 1000
export const knowledgeDocumentTitleMaxLength = 120
export const knowledgeEmbeddingDimensions = [384, 512, 768, 1024, 1536, 2048, 3072]

/** 校验数字输入框中的必填整数与取值范围。 */
function integerField(message: string, min: number, max = Number.MAX_SAFE_INTEGER) {
  return z.string().refine((value) => {
    const number = Number(value)
    return value.trim() !== "" && Number.isSafeInteger(number) && number >= min && number <= max
  }, message)
}

/** 按知识库类型校验基础信息和模型配置。 */
export function createKnowledgeBaseSchema(
  messages: {
    nameRequired: string
    nameTooLong: string
    descriptionTooLong: string
    embeddingModelRequired: string
    embeddingDimensionInvalid: string
    chunkLengthInvalid: string
    chunkOverlapInvalid: string
    retrievalCountInvalid: string
    retrievalScoreThresholdInvalid: string
    rerankModelRequired: string
  },
  isQA: boolean,
) {
  return z.object({
    name: z.string().trim().min(1, messages.nameRequired).max(knowledgeBaseNameMaxLength, messages.nameTooLong),
    description: z.string().trim().max(knowledgeBaseDescriptionMaxLength, messages.descriptionTooLong),
    embeddingModel: z.string().min(1, messages.embeddingModelRequired),
    embeddingDimension: z.string().refine((value) => knowledgeEmbeddingDimensions.includes(Number(value)), messages.embeddingDimensionInvalid),
    chunkLength: isQA ? z.string() : integerField(messages.chunkLengthInvalid, 256, 2048),
    chunkOverlap: isQA ? z.string() : integerField(messages.chunkOverlapInvalid, 0, 200),
    retrievalCount: integerField(messages.retrievalCountInvalid, 1, 20),
    // 相关性阈值取 0 至 1 之间的数值。
    retrievalScoreThreshold: z.string().refine((value) => value.trim() !== "" && Number(value) >= 0 && Number(value) <= 1, messages.retrievalScoreThresholdInvalid),
    rerankModel: z.string().min(1, messages.rerankModelRequired),
  })
}

export type KnowledgeBaseFormValues = z.infer<ReturnType<typeof createKnowledgeBaseSchema>>
