/** 验证知识库配置的数值边界、必填字段与问答库差异。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { createKnowledgeBaseSchema } from "../src/features/knowledge-base/knowledge-base-schema.ts"

const messages = {
  nameRequired: "nameRequired", nameTooLong: "nameTooLong", descriptionTooLong: "descriptionTooLong",
  embeddingModelRequired: "embeddingModelRequired", embeddingDimensionInvalid: "embeddingDimensionInvalid",
  chunkLengthInvalid: "chunkLengthInvalid", chunkOverlapInvalid: "chunkOverlapInvalid", retrievalCountInvalid: "retrievalCountInvalid",
  retrievalScoreThresholdInvalid: "retrievalScoreThresholdInvalid", rerankModelRequired: "rerankModelRequired",
}
const valid = {
  name: "知识库", description: "", embeddingModel: '["provider","embedding"]', embeddingDimension: "1024",
  chunkLength: "512", chunkOverlap: "50", retrievalCount: "3", retrievalScoreThreshold: "0.7", rerankModel: '["provider","rerank"]',
}

test("文档库必填数值接收边界值，拒绝空值、非法格式和越界值", () => {
  const schema = createKnowledgeBaseSchema(messages, false)
  assert.equal(schema.safeParse(valid).success, true)
  assert.equal(schema.safeParse({ ...valid, chunkLength: "256", chunkOverlap: "0", retrievalCount: "1", retrievalScoreThreshold: "0" }).success, true)
  assert.equal(schema.safeParse({ ...valid, chunkLength: "2048", chunkOverlap: "200", retrievalCount: "20", retrievalScoreThreshold: "1" }).success, true)
  assert.equal(schema.safeParse({ ...valid, embeddingDimension: "3072" }).success, true)
  for (const [field, values] of Object.entries({
    embeddingDimension: ["", "0", "-1", "1.5", "1000", "4096"],
    chunkLength: ["", "255", "2049", "512.5"],
    chunkOverlap: ["", "-1", "201", "0.5"],
    retrievalCount: ["", "0", "21", "3.5"],
    retrievalScoreThreshold: ["", "-0.1", "1.01", "abc"],
  })) {
    for (const value of values) {
      const result = schema.safeParse({ ...valid, [field]: value })
      assert.equal(result.success, false, `${field}=${value}`)
      if (!result.success) assert.equal(result.error.issues[0].message, `${field}Invalid`)
    }
  }
  assert.equal(schema.safeParse({ ...valid, embeddingModel: "" }).success, false)
  const missingRerank = schema.safeParse({ ...valid, rerankModel: "" })
  assert.equal(missingRerank.success, false)
  if (!missingRerank.success) assert.equal(missingRerank.error.issues[0].message, "rerankModelRequired")
})

test("问答库无需分段参数，重排模型同样必填", () => {
  const schema = createKnowledgeBaseSchema(messages, true)
  const input = { ...valid, chunkLength: "", chunkOverlap: "" }
  assert.equal(schema.safeParse(input).success, true)
  assert.equal(schema.safeParse({ ...input, rerankModel: "" }).success, false)
})
