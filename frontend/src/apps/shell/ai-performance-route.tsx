/** Shell 路由：AI 表现页注入待补知识侧栏。 */
import { AIKnowledgeGapSheet } from "@/features/knowledge-gaps/public"
import { AIPerformancePage } from "@/features/reports/public"

/** 渲染 AI 表现报表并装配待补知识处理侧栏。 */
export function AIPerformanceRoute() {
  return <AIPerformancePage KnowledgeGapSheet={AIKnowledgeGapSheet} />
}
