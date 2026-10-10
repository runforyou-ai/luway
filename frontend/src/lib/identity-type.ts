/** 企业身份类型的判定。 */
import { WorkspaceIdentityType } from "@/api"

/** 判断企业身份是否为 AI 员工。 */
export function isAIIdentityType(type: WorkspaceIdentityType | null | undefined) {
  return type === WorkspaceIdentityType.Agent
}
