/** AI 员工表单校验规则。 */
import { z } from "zod"

import { AgentExecutionMode, ServiceAudience, WorkStatus } from "@/api"
import { createAgentManagedExecutionSchema } from "@/lib/agent-execution-schema"
import { displayNamePattern } from "@/lib/display-name"
import { requiredWailsEnum } from "@/lib/wails-enum"

/** AI 员工表单校验文案。 */
interface AgentValidationMessages {
  nameRequired: string
  nameInvalid: string
  modelRequired: string
  instructionTooLong: string
}

/** 创建 AI 员工资料校验规则。 */
export function createAgentProfileSchema(messages: {
  nameRequired: string
  nameInvalid: string
}) {
  return z.object({
    displayName: z
      .string()
      .trim()
      .min(1, messages.nameRequired)
      .regex(displayNamePattern, messages.nameInvalid),
    workStatus: requiredWailsEnum(WorkStatus),
    teamIds: z.array(z.string().uuid()),
    serviceAudiences: z.array(requiredWailsEnum(ServiceAudience)),
    handoffTeamId: z.string(),
    responsibleUserId: z.string(),
  })
}

/** 创建新增 AI 员工表单校验规则。 */
export function createAgentSchema(messages: AgentValidationMessages) {
  return createAgentProfileSchema(messages)
    .omit({ workStatus: true, handoffTeamId: true, responsibleUserId: true })
    .extend({
      execution: z.object({
        mode: z.literal(AgentExecutionMode.AgentExecutionModeManaged),
        managed: createAgentManagedExecutionSchema(messages),
      }),
    })
}

export type AgentProfileFormValues = z.infer<
  ReturnType<typeof createAgentProfileSchema>
>

/** 创建运行配置编辑表单校验规则，服务绑定仅在编辑页配置。 */
export function createAgentExecutionSchema(
  messages: Omit<AgentValidationMessages, "nameRequired" | "nameInvalid">,
) {
  return createAgentManagedExecutionSchema(messages).extend({ mcpServerIds: z.array(z.string().uuid()) })
}

export type AgentExecutionFormValues = z.infer<ReturnType<typeof createAgentExecutionSchema>>

export type AgentFormValues = z.infer<ReturnType<typeof createAgentSchema>>
