/** AI 员工模型选择值的编码和校验。 */
import { z } from "zod"

const agentModelSelectionSchema = z.tuple([
  z.string().uuid(),
  z.string().trim().min(1),
])

/** 创建模型选择值。 */
export function agentModelSelection(
  providerId: string,
  modelIdentifier: string,
) {
  return JSON.stringify([providerId, modelIdentifier])
}

/** 判断模型选择值是否包含供应商和模型标识。 */
export function isAgentModelSelection(value: string) {
  try {
    return agentModelSelectionSchema.safeParse(JSON.parse(value)).success
  } catch {
    return false
  }
}

/** 解析经过表单校验的模型选择值。 */
export function parseAgentModelSelection(value: string) {
  const [providerId, modelIdentifier] = agentModelSelectionSchema.parse(
    JSON.parse(value),
  )
  return { providerId, modelIdentifier }
}
