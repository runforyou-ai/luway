/** AI 员工模块的导航地址：表单返回地址与本人负责的待补知识。 */

/** AI 表现页 AI 员工筛选中表示本人负责的 AI 员工的取值。 */
export const mineAgentFilter = "mine"

/** AI 表现页中本人负责的 AI 员工的待处理待补知识。 */
export const responsibleKnowledgeGapsPath = `/ai-performance?tab=knowledgeGaps&agent=${mineAgentFilter}`

/** 保留来源查询条件，并将返回目标限定为 AI 员工列表或团队页。 */
export function agentReturnPath(pathname: string, search: string) {
  const params = new URLSearchParams(search)
  const candidate = params.get("returnTo") ?? `${pathname}${search}`
  const candidatePath = candidate.split("?")[0]
  return /^\/(ai-employees|contacts\/teams\/[^/?#]+)$/.test(candidatePath)
    ? candidate
    : "/ai-employees"
}
