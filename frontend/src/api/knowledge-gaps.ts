/** 待补知识调用。 */
import * as ops from "@/api/generated/operations"

/** 读取一页指定处理状态的待补知识。 */
export const listKnowledgeGaps = ops.listKnowledgeGaps

/** 读取待补知识详情。 */
export const getKnowledgeGap = ops.getKnowledgeGap

/** 把待补知识整理的问答加入知识库。 */
export const acceptKnowledgeGap = ops.acceptKnowledgeGap

/** 忽略待补知识。 */
export const dismissKnowledgeGap = ops.dismissKnowledgeGap
