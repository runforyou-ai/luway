/** 服务设置子表单共享当前配置的服务对象。 */
import { createContext, useContext } from "react"
import type { ServiceAudience } from "@/api"

export const ServicePolicyAudienceContext = createContext<ServiceAudience | undefined>(undefined)

/** 读取设置页面显式指定的服务对象。 */
export function useServicePolicyAudience() {
  const audience = useContext(ServicePolicyAudienceContext)
  if (!audience) throw new Error("Service policy audience is required")
  return audience
}
