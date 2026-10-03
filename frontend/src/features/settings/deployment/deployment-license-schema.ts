/** 实例授权激活表单校验规则。 */
import { z } from "zod"

type LicenseTranslator = (key: "license.validation.licenseCodeRequired") => string

/** 创建实例授权激活表单校验，授权码去除首尾空白后不能为空。 */
export function createLicenseActivationSchema(t: LicenseTranslator) {
  return z.object({
    licenseCode: z.string().trim().min(1, t("license.validation.licenseCodeRequired")),
  })
}

export type LicenseActivationFormValues = z.infer<ReturnType<typeof createLicenseActivationSchema>>
