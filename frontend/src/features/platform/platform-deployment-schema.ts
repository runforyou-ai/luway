/** 部署配置各页签的表单校验规则，与服务端校验保持一致。 */
import { z } from "zod"

import { PlatformCertificateSource } from "@/api"
import { isHTTPEndpoint, isHTTPOrigin } from "@/lib/http-url"

type DeploymentTranslator = (
  key:
    | "deployment.validation.publicURLInvalid"
    | "deployment.validation.certificateRequired"
    | "deployment.validation.privateKeyRequired"
    | "deployment.validation.urlInvalid"
    | "deployment.validation.storageRequired"
    | "deployment.validation.portInvalid"
    | "deployment.validation.fromAddressInvalid"
    | "deployment.validation.credentialsIncomplete"
    | "deployment.validation.sdkNameInvalid",
) => string

/** 部署名称与产品名称的最大字符数。 */
const nameMaxLength = 64

/** 去掉首尾空白与末尾斜杠。 */
function trimURL(value: string) {
  return value.trim().replace(/\/+$/, "")
}

/** 部署名称、平台时区与上报开关的表单校验。 */
export const basicsSchema = z.object({
  name: z.string().trim().max(nameMaxLength),
  timeZone: z.string().min(1),
  telemetryEnabled: z.boolean(),
})

/** 判断部署地址是否使用 HTTPS。 */
export function isHTTPSAddress(publicURL: string) {
  return publicURL.trim().toLowerCase().startsWith("https://")
}

/** 创建部署地址与证书的表单校验：httpsServers 为真、部署地址为 HTTPS 且上传证书时，证书链与私钥必填。 */
export function createAddressSchema(t: DeploymentTranslator, httpsServers: boolean) {
  return z
    .object({
      publicURL: z
        .string()
        .transform(trimURL)
        .pipe(z.string().min(1).refine(isHTTPOrigin, t("deployment.validation.publicURLInvalid"))),
      source: z.enum([PlatformCertificateSource.ACME, PlatformCertificateSource.Upload]),
      certificate: z.string().trim(),
      privateKey: z.string().trim(),
    })
    .superRefine((values, context) => {
      if (!httpsServers || !isHTTPSAddress(values.publicURL) || values.source !== PlatformCertificateSource.Upload) return
      if (!values.certificate) context.addIssue({ code: "custom", path: ["certificate"], message: t("deployment.validation.certificateRequired") })
      if (!values.privateKey) context.addIssue({ code: "custom", path: ["privateKey"], message: t("deployment.validation.privateKeyRequired") })
    })
}

/** 创建对象存储的表单校验：开启时接口地址与公开地址为完整地址，区域、存储桶与访问密钥必填。 */
export function createStorageSchema(t: DeploymentTranslator) {
  return z
    .object({
      enabled: z.boolean(),
      endpoint: z.string().transform(trimURL),
      publicBaseURL: z.string().transform(trimURL),
      region: z.string().trim(),
      bucket: z.string().trim(),
      accessKeyID: z.string().trim(),
      secretAccessKey: z.string().trim(),
      forcePathStyle: z.boolean(),
    })
    .superRefine((values, context) => {
      if (!values.enabled) return
      for (const field of ["endpoint", "publicBaseURL"] as const) {
        if (!isHTTPEndpoint(values[field])) context.addIssue({ code: "custom", path: [field], message: t("deployment.validation.urlInvalid") })
      }
      for (const field of ["region", "bucket", "accessKeyID", "secretAccessKey"] as const) {
        if (!values[field]) context.addIssue({ code: "custom", path: [field], message: t("deployment.validation.storageRequired") })
      }
    })
}

/** 创建邮件发送的表单校验：主机非空时端口在 1 到 65535 之间、发件邮箱有效，用户名与密码同时填写。 */
export function createEmailSchema(t: DeploymentTranslator) {
  return z
    .object({
      host: z.string().trim(),
      port: z.string().trim(),
      security: z.string(),
      username: z.string().trim(),
      password: z.string(),
      fromAddress: z.string().trim(),
    })
    .superRefine((values, context) => {
      if (!values.host) return
      const port = Number(values.port)
      if (!Number.isInteger(port) || port < 1 || port > 65535) {
        context.addIssue({ code: "custom", path: ["port"], message: t("deployment.validation.portInvalid") })
      }
      if (!z.email().safeParse(values.fromAddress).success) {
        context.addIssue({ code: "custom", path: ["fromAddress"], message: t("deployment.validation.fromAddressInvalid") })
      }
      if ((values.username === "") !== (values.password === "")) {
        context.addIssue({ code: "custom", path: ["password"], message: t("deployment.validation.credentialsIncomplete") })
      }
    })
}

/** 创建部署品牌的表单校验：产品名称不超过 64 个字符，对象名以字母开头且只含字母和数字。 */
export function createBrandingSchema(t: DeploymentTranslator) {
  return z.object({
    names: z.record(z.string(), z.string().trim().max(nameMaxLength)),
    sdkName: z
      .string()
      .trim()
      .refine((value) => value === "" || /^[A-Za-z][A-Za-z0-9]*$/.test(value), t("deployment.validation.sdkNameInvalid")),
    icon: z.string(),
  })
}

/** 创建产品首页配置的表单校验。 */
export function createHomeSchema() {
  return z.object({ selfHost: z.boolean() })
}

export type BasicsFormValues = z.infer<typeof basicsSchema>

export type AddressFormValues = z.infer<ReturnType<typeof createAddressSchema>>

export type StorageFormValues = z.infer<ReturnType<typeof createStorageSchema>>

export type EmailFormValues = z.infer<ReturnType<typeof createEmailSchema>>

export type BrandingFormValues = z.infer<ReturnType<typeof createBrandingSchema>>

export type HomeFormValues = z.infer<ReturnType<typeof createHomeSchema>>
