/** 角色设置表单校验规则。 */
import { z } from "zod"

import { PermissionCode } from "@/api"

/** 自定义角色名称允许的最大字符数。 */
export const roleNameMaxLength = 10

/** 创建角色设置表单校验。 */
export function createRoleSettingsSchema(messages: {
  nameRequired: string
  nameTooLong: string
  descriptionTooLong: string
}) {
  return z.object({
    name: z
      .string()
      .trim()
      .min(1, messages.nameRequired)
      .max(roleNameMaxLength, messages.nameTooLong),
    description: z
      .string()
      .trim()
      .max(200, messages.descriptionTooLong),
    permissions: z.array(z.nativeEnum(PermissionCode)),
  })
}

export type RoleSettingsFormValues = z.infer<
  ReturnType<typeof createRoleSettingsSchema>
>
