/** 授权在线激活与离线授权表单校验规则。 */
import { z } from "zod"

/** 在线激活表单校验，激活码去除首尾空白后不能为空。 */
export const onlineActivationSchema = z.object({
  activationCode: z.string().trim().min(1),
})

/** 离线授权表单校验，授权码去除首尾空白后不能为空。 */
export const licenseActivationSchema = z.object({
  licenseCode: z.string().trim().min(1),
})

export type OnlineActivationFormValues = z.infer<typeof onlineActivationSchema>

export type LicenseActivationFormValues = z.infer<typeof licenseActivationSchema>
