/** 企业身份类型的判定。 */
import { OrganizationIdentityType } from "@/api"

/** 判断企业身份是否为 AI 员工或助理。 */
export function isAIIdentityType(type: OrganizationIdentityType | null | undefined) {
  return (
    type === OrganizationIdentityType.OrganizationIdentityTypeAgent ||
    type === OrganizationIdentityType.OrganizationIdentityTypeAssistant
  )
}
