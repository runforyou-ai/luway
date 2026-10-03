/** 授权在线激活与离线授权表单校验规则。 */
import { z } from "zod"

type LicenseTranslator = (key: "license.validation.activationCodeRequired" | "license.validation.licenseCodeRequired") => string

/** 创建在线激活表单校验，激活码去除首尾空白后不能为空。 */
export function createOnlineActivationSchema(t: LicenseTranslator) {
  return z.object({
    activationCode: z.string().trim().min(1, t("license.validation.activationCodeRequired")),
  })
}

/** 创建离线授权表单校验，授权码去除首尾空白后不能为空。 */
export function createLicenseActivationSchema(t: LicenseTranslator) {
  return z.object({
    licenseCode: z.string().trim().min(1, t("license.validation.licenseCodeRequired")),
  })
}

export type OnlineActivationFormValues = z.infer<ReturnType<typeof createOnlineActivationSchema>>

export type LicenseActivationFormValues = z.infer<ReturnType<typeof createLicenseActivationSchema>>
