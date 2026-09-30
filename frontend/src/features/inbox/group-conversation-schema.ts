/** 群聊创建与资料编辑共用的字段校验和人数限制。 */
import { z } from "zod"

export const groupTitleMaxLength = 100
export const groupDescriptionMaxLength = 500
export const groupMemberMaxCount = 100
export const groupAdditionalMemberMaxCount = groupMemberMaxCount - 1

type GroupTranslator = (
  key:
    | "groupTitleTooLong"
    | "groupDescriptionTooLong"
    | "groupMembersRequired"
    | "groupMembersTooMany",
) => string

/** 校验群名称和描述。 */
export function createGroupProfileSchema(t: GroupTranslator) {
  return z.object({
    title: z
      .string()
      .trim()
      .max(groupTitleMaxLength, t("groupTitleTooLong")),
    description: z
      .string()
      .trim()
      .max(groupDescriptionMaxLength, t("groupDescriptionTooLong")),
  })
}

/** 按各端的成员表单值组合建群校验。 */
export function createGroupConversationSchema<T extends z.ZodType>(
  t: GroupTranslator,
  memberSchema: T,
) {
  return createGroupProfileSchema(t).extend({
    members: z
      .array(memberSchema)
      .min(1, t("groupMembersRequired"))
      .max(groupAdditionalMemberMaxCount, t("groupMembersTooMany")),
  })
}
