/** 个人 AI 员工表单校验规则。 */
import { z } from "zod"

import { createAgentManagedExecutionSchema } from "@/lib/agent-execution-schema"
import { isAgentModelSelection } from "@/lib/agent-model-selection"
import { displayNamePattern } from "@/lib/display-name"

/** 创建个人 AI 员工资料与执行配置校验规则，localAgent 为空表示由个人 AI 员工自己完成，此时必须选择对话模型。 */
export function createPersonalAgentSchema(messages: {
  nameRequired: string
  nameInvalid: string
  modelRequired: string
  instructionTooLong: string
}) {
  return createAgentManagedExecutionSchema(messages)
    .extend({
      displayName: z
        .string()
        .trim()
        .min(1, messages.nameRequired)
        .regex(displayNamePattern, messages.nameInvalid),
      modelSelection: z.string(),
      localAgent: z.string(),
      mcpServerIds: z.array(z.string().uuid()),
    })
    .superRefine((values, context) => {
      if (!values.localAgent && !isAgentModelSelection(values.modelSelection)) {
        context.addIssue({ code: "custom", path: ["modelSelection"], message: messages.modelRequired })
      }
    })
}

export type PersonalAgentFormValues = z.infer<ReturnType<typeof createPersonalAgentSchema>>
