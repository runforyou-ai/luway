/** 站点设置长表单校验，与服务端校验保持一致。 */
import { z } from "zod"

import { PlatformCertificateSource, RegistrationPolicy } from "@/api"
import { isHTTPOrigin } from "@/lib/http-url"

type SiteTranslator = (
  key:
    | "deployment.validation.publicURLInvalid"
    | "deployment.validation.certificateRequired"
    | "deployment.validation.privateKeyRequired"
    | "deployment.validation.sdkNameInvalid",
) => string

/** 部署名称与产品名称的最大字符数。 */
const nameMaxLength = 64

/** 去掉首尾空白与末尾斜杠。 */
function trimURL(value: string) {
  return value.trim().replace(/\/+$/, "")
}

/** 判断部署地址是否使用 HTTPS。 */
export function isHTTPSAddress(publicURL: string) {
  return publicURL.trim().toLowerCase().startsWith("https://")
}

/** 创建站点设置表单校验：httpsServers 为真、部署地址为 HTTPS 且上传证书时，证书链与私钥必填。 */
export function createSiteSchema(t: SiteTranslator, httpsServers: boolean) {
  return z
    .object({
      name: z.string().trim().max(nameMaxLength),
      timeZone: z.string().min(1),
      registrationPolicy: z.enum([RegistrationPolicy.Open, RegistrationPolicy.InvitationOnly]),
      workspaceCreationQuota: z.number().int().min(0),
      publicURL: z
        .string()
        .transform(trimURL)
        .pipe(z.string().min(1).refine(isHTTPOrigin, t("deployment.validation.publicURLInvalid"))),
      source: z.enum([PlatformCertificateSource.ACME, PlatformCertificateSource.Upload]),
      certificate: z.string().trim(),
      privateKey: z.string().trim(),
      names: z.record(z.string(), z.string().trim().max(nameMaxLength)),
      sdkName: z
        .string()
        .trim()
        .refine((value) => value === "" || /^[A-Za-z][A-Za-z0-9]*$/.test(value), t("deployment.validation.sdkNameInvalid")),
      icon: z.string(),
    })
    .superRefine((values, context) => {
      if (!httpsServers || !isHTTPSAddress(values.publicURL) || values.source !== PlatformCertificateSource.Upload) return
      if (!values.certificate) context.addIssue({ code: "custom", path: ["certificate"], message: t("deployment.validation.certificateRequired") })
      if (!values.privateKey) context.addIssue({ code: "custom", path: ["privateKey"], message: t("deployment.validation.privateKeyRequired") })
    })
}

export type SiteFormValues = z.infer<ReturnType<typeof createSiteSchema>>
