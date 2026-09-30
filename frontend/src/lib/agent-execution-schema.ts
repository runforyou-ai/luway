/** AI 员工与助理共用的托管执行配置校验。 */
import { z } from "zod"

import { isAgentModelSelection } from "@/lib/agent-model-selection"

const maxSystemInstructionLength = 20000

/** 创建 AI 员工平台托管执行配置校验规则。 */
export function createAgentManagedExecutionSchema(
  messages: { modelRequired: string; instructionTooLong: string },
) {
  return z.object({
    modelSelection: z
      .string()
      .min(1, messages.modelRequired)
      .refine(isAgentModelSelection, messages.modelRequired),
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
