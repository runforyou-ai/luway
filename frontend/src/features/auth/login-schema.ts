/** 登录表单校验规则。 */
import { z } from "zod"

/** 登录表单校验。 */
export const loginSchema = z.object({
  email: z.string().trim().min(1).email(),
  password: z.string().min(1),
})

export type LoginFormValues = z.infer<typeof loginSchema>
