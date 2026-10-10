/** 工作区通用设置表单校验规则。 */
import { z } from "zod"

import { workspaceNameField } from "@/lib/workspace-schema"

/** 工作区通用设置校验，工作区标识创建后不修改。 */
export const generalSettingsSchema = z.object({
  name: workspaceNameField,
})

export type GeneralSettingsFormValues = z.infer<typeof generalSettingsSchema>
