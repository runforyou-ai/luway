/**
 * reports 对外入口：宿主与其他 feature 只从本文件取符号。
 * 通用指标卡/周期筛选请直接用 @/components/metrics。
 */
export { AIPerformancePage } from "./ai-performance-page"
export { TeamPerformancePage } from "./team-performance-page"
export { AIPerformanceOverview } from "./ai-performance-overview"
export { AIKnowledgeGapList, AIPerformanceIssueList } from "./ai-performance-lists"
export { aiIssueTypes, gapStatuses } from "./ai-performance-format"
export { useReportSearchParams } from "./report-filters"
