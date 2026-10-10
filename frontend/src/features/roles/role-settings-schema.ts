/** 角色设置表单校验规则。 */
import { z } from "zod"

import { PermissionCode } from "@/api"

/** 自定义角色名称允许的最大字符数。 */
export const roleNameMaxLength = 10

/** 角色设置表单校验。 */
export const roleSettingsSchema = z.object({
  name: z.string().trim().min(1).max(roleNameMaxLength),
  description: z.string().trim().max(200),
  permissions: z.array(z.nativeEnum(PermissionCode)),
})

export type RoleSettingsFormValues = z.infer<typeof roleSettingsSchema>
