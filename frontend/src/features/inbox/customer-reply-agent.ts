/** 客服回复场景的 AI 员工默认选择规则。 */

/** 偏好的 AI 员工仍在可选列表中时选它，否则选列表第一个，列表为空时返回空编号。 */
export function selectServiceReplyAgentID(
  agents: readonly { identityId: string }[],
  preferredID: string,
) {
  return agents.some((agent) => agent.identityId === preferredID)
    ? preferredID
    : (agents[0]?.identityId ?? "")
}
