/** AI 员工表单校验规则。 */
import { z } from "zod"

import { AgentExecutionMode, ServiceAudience, WorkStatus, computerOperationLevels } from "@/api"
import { businessSystemGrantsSchema, createAgentManagedExecutionSchema } from "@/lib/agent-execution-schema"
import { displayNamePattern } from "@/lib/display-name"
import { enumSchema } from "@/lib/enum"

/** AI 员工表单校验文案。 */
interface AgentValidationMessages {
  nameInvalid: string
  instructionTooLong: string
}

/** AI 员工资料字段的校验规则。 */
function agentProfileFields(messages: { nameInvalid: string }) {
  return z.object({
    displayName: z
      .string()
      .trim()
      .min(1)
      .regex(displayNamePattern, messages.nameInvalid),
    workStatus: enumSchema(WorkStatus),
    teamIds: z.array(z.string().uuid()),
    serviceAudiences: z.array(enumSchema(ServiceAudience)),
    handoffTeamId: z.string(),
    responsibleUserId: z.string(),
    computerId: z.string(),
    computerGrant: z.object({ maxLevel: z.enum(computerOperationLevels), confirmL2: z.boolean() }),
    localAgents: z.array(z.string()),
  })
}

/** 创建 AI 员工资料校验规则。 */
export function createAgentProfileSchema(messages: { nameInvalid: string }) {
  return agentProfileFields(messages)
}

/** 创建新增 AI 员工表单校验规则。 */
export function createAgentSchema(messages: AgentValidationMessages) {
  return agentProfileFields(messages)
    .omit({ workStatus: true, handoffTeamId: true, responsibleUserId: true, computerId: true, computerGrant: true, localAgents: true })
    .extend({
      execution: z.object({
        mode: z.literal(AgentExecutionMode.Managed),
        managed: createAgentManagedExecutionSchema(messages),
      }),
    })
}

export type AgentProfileFormValues = z.infer<
  ReturnType<typeof createAgentProfileSchema>
>

/** 创建运行配置编辑表单校验规则，业务系统授权仅在编辑页配置。 */
export function createAgentExecutionSchema(
  messages: Omit<AgentValidationMessages, "nameInvalid">,
) {
  return createAgentManagedExecutionSchema(messages).extend({ businessSystems: businessSystemGrantsSchema })
}

export type AgentExecutionFormValues = z.infer<ReturnType<typeof createAgentExecutionSchema>>

export type AgentFormValues = z.infer<ReturnType<typeof createAgentSchema>>
