/** 工作区名称的表单校验规则，创建工作区、首次安装和工作区设置共用。 */
import { z } from "zod"

/** 工作区字段校验使用的文案。 */
type WorkspaceFieldMessages = {
  nameRequired: string
  nameTooLong: string
}

/** 创建工作区名称的校验规则。 */
export function workspaceNameField(messages: WorkspaceFieldMessages) {
  return z.string().trim().min(1, messages.nameRequired).max(32, messages.nameTooLong)
}

/** 创建新建工作区表单校验。 */
export function createWorkspaceSchema(messages: WorkspaceFieldMessages) {
  return z.object({
    name: workspaceNameField(messages),
  })
}

export type WorkspaceFormValues = z.infer<ReturnType<typeof createWorkspaceSchema>>
