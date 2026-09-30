/** 团队表单校验规则。 */
import { z } from "zod"

/** 创建团队表单校验规则。 */
export function createTeamSchema(messages: {
  nameRequired: string
  nameTooLong: string
  descriptionTooLong: string
}) {
  return z.object({
    name: z
      .string()
      .trim()
      .min(1, messages.nameRequired)
      .max(64, messages.nameTooLong),
    description: z.string().trim().max(500, messages.descriptionTooLong),
  })
}

export type TeamFormValues = z.infer<ReturnType<typeof createTeamSchema>>
