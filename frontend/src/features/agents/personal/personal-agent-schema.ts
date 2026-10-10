/** 个人 AI 员工表单校验规则。 */
import { z } from "zod"

import { businessSystemGrantsSchema, createAgentManagedExecutionSchema } from "@/lib/agent-execution-schema"
import { displayNamePattern } from "@/lib/display-name"

/** 创建个人 AI 员工资料与执行配置校验规则。 */
export function createPersonalAgentSchema(messages: {
  nameInvalid: string
  instructionTooLong: string
}) {
  return createAgentManagedExecutionSchema(messages)
    .extend({
      displayName: z
        .string()
        .trim()
        .min(1)
        .regex(displayNamePattern, messages.nameInvalid),
      businessSystems: businessSystemGrantsSchema,
      localAgents: z.array(z.string()),
    })
}

export type PersonalAgentFormValues = z.infer<ReturnType<typeof createPersonalAgentSchema>>
