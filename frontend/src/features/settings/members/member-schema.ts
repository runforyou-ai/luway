/** 工作区成员表单校验规则。 */
import { z } from "zod"

import { displayNamePattern } from "@/lib/display-name"

/** 创建工作区成员编辑表单校验规则，邮箱只读展示不参与校验。 */
export function createMemberSchema(messages: {
  nameRequired: string
  nameInvalid: string
  roleRequired: string
  maxServiceSessionsInvalid: string
}) {
  return z.object({
    displayName: z
      .string()
      .trim()
      .min(1, messages.nameRequired)
      .regex(displayNamePattern, messages.nameInvalid),
    email: z.string(),
    roleId: z.string().uuid(messages.roleRequired),
    teamIds: z.array(z.string().uuid()),
    handlesServiceRequests: z.boolean(),
    maxServiceSessions: z.string().trim(),
  }).superRefine((values, context) => {
    // 最大接待量只在开启接待时校验，必须为正整数。
    if (values.handlesServiceRequests && !/^[1-9]\d*$/.test(values.maxServiceSessions)) {
      context.addIssue({
        code: "custom",
        path: ["maxServiceSessions"],
        message: messages.maxServiceSessionsInvalid,
      })
    }
  })
}

export type MemberFormValues = z.infer<ReturnType<typeof createMemberSchema>>
