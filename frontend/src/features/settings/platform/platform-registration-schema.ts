/** 注册与创建设置表单校验规则。 */
import { z } from "zod"

import { RegistrationPolicy, WorkspaceCreationPolicy } from "@/api"

/** 注册与创建设置表单校验。 */
export const registrationSettingsSchema = z.object({
  registrationPolicy: z.enum([RegistrationPolicy.RegistrationPolicyOpen, RegistrationPolicy.RegistrationPolicyInvitationOnly]),
  workspaceCreationPolicy: z.enum([
    WorkspaceCreationPolicy.WorkspaceCreationPolicyAnyAccount,
    WorkspaceCreationPolicy.WorkspaceCreationPolicyPlatformAdmin,
  ]),
})

export type RegistrationSettingsFormValues = z.infer<typeof registrationSettingsSchema>
