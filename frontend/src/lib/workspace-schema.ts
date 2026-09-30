/** 工作区名称与标识的表单校验规则，创建工作区、首次安装和工作区设置共用。 */
import { z } from "zod"

const workspaceSlugPattern = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/

/** 工作区字段校验使用的文案。 */
type WorkspaceFieldMessages = {
  nameRequired: string
  nameTooLong: string
  slugRequired: string
  slugInvalid: string
}

/** 创建工作区名称的校验规则。 */
export function workspaceNameField(messages: Pick<WorkspaceFieldMessages, "nameRequired" | "nameTooLong">) {
  return z.string().trim().min(1, messages.nameRequired).max(32, messages.nameTooLong)
}

/** 创建工作区标识的校验规则，校验前去除空白并转为小写。 */
export function workspaceSlugField(messages: WorkspaceFieldMessages) {
  return z
    .string()
    .trim()
    .toLowerCase()
    .min(1, messages.slugRequired)
    .regex(workspaceSlugPattern, messages.slugInvalid)
}

/** 创建新建工作区表单校验。 */
export function createWorkspaceSchema(messages: WorkspaceFieldMessages) {
  return z.object({
    name: workspaceNameField(messages),
    slug: workspaceSlugField(messages),
  })
}

export type WorkspaceFormValues = z.infer<ReturnType<typeof createWorkspaceSchema>>

/** 按工作区名称建议标识：保留小写字母和数字，其余连续字符转为连字符；名称没有可用字符（如中文名称）时使用 fallback。 */
export function suggestWorkspaceSlug(name: string, fallback: string) {
  const slug = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63)
    .replace(/-+$/g, "")
  return slug || (name.trim() ? fallback : "")
}

/** 生成随机的默认工作区标识，用于名称无法转成标识的情况。 */
export function randomWorkspaceSlug() {
  const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
  const bytes = crypto.getRandomValues(new Uint8Array(8))
  return `ws-${Array.from(bytes, (byte) => alphabet[byte % alphabet.length]).join("")}`
}
