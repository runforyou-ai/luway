/** 工作区名称的表单校验规则，创建工作区、首次安装和工作区设置共用。 */
import { z } from "zod"

/** 工作区名称的校验规则。 */
export const workspaceNameField = z.string().trim().min(1).max(32)

/** 新建工作区表单校验。 */
export const workspaceSchema = z.object({
  name: workspaceNameField,
})

export type WorkspaceFormValues = z.infer<typeof workspaceSchema>
