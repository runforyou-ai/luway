/** 工作区通用设置表单校验规则。 */
import { z } from "zod"

import { workspaceNameField } from "@/lib/workspace-schema"

/** 创建工作区通用设置校验，工作区标识创建后不修改。 */
export function createGeneralSettingsSchema(messages: {
  nameRequired: string
  nameTooLong: string
}) {
  return z.object({
    name: workspaceNameField(messages),
  })
}

export type GeneralSettingsFormValues = z.infer<
  ReturnType<typeof createGeneralSettingsSchema>
>
