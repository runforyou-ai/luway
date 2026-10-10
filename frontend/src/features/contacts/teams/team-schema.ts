/** 团队表单校验规则。 */
import { z } from "zod"

/** 团队表单校验规则。 */
export const teamSchema = z.object({
  name: z.string().trim().min(1).max(64),
  description: z.string().trim().max(500),
})

export type TeamFormValues = z.infer<typeof teamSchema>
