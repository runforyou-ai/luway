/** AI 员工共用的托管执行配置与业务系统授权校验。 */
import { z } from "zod"

import { operationLevels } from "@/api"

const maxSystemInstructionLength = 20000

/** 创建 AI 员工平台托管执行配置校验规则。 */
export function createAgentManagedExecutionSchema(messages: { instructionTooLong: string }) {
  return z.object({
    modelId: z.string().min(1),
    knowledgeBaseIds: z.array(z.string().uuid()),
    systemInstruction: z
      .string()
      .trim()
      .refine((value) => {
        // 校验系统指令的 Unicode 字符数上限。
        return [...value].length <= maxSystemInstructionLength
      }, messages.instructionTooLong),
  })
}

/** AI 员工的业务系统授权校验规则。 */
export const businessSystemGrantsSchema = z.array(
  z.object({
    id: z.string().uuid(),
    maxLevel: z.enum(operationLevels),
    confirmL2: z.boolean(),
    outbound: z.boolean(),
  }),
)
