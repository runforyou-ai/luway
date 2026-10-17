/** 邮件接入表单的草稿、密钥保留状态和按服务商校验。 */
import { z } from "zod"
import { EmailProvider, type PlatformEmailSettings } from "@/api"

/** 将读取结果变成当前服务商配置与其他服务商的空草稿。 */
export function emailFormValues(email: PlatformEmailSettings) {
  return {
    enabled: email.enabled, provider: email.provider, fromAddress: email.fromAddress, fromName: email.fromName,
    smtp: { host: email.smtp?.host ?? "", port: String(email.smtp?.port ?? 587), security: email.smtp?.security ?? "starttls", username: email.smtp?.username ?? "", password: "" },
    ses: { region: email.ses?.region ?? "", accessKeyID: email.ses?.accessKeyID ?? "", secretAccessKey: "" },
    mailgun: { domain: email.mailgun?.domain ?? "", region: email.mailgun?.region ?? "us", apiKey: "" },
    postmark: { messageStream: email.postmark?.messageStream ?? "", serverToken: "" },
    resend: { apiKey: "" },
  }
}

/** 邮件表单包含五种方式的内存草稿，保存时只提交当前一种。 */
export type EmailFormValues = ReturnType<typeof emailFormValues>

type Translator = (key: "deployment.validation.emailSettingInvalid" | "deployment.validation.fromAddressInvalid" | "deployment.validation.portInvalid" | "deployment.validation.credentialsIncomplete") => string

/** 判断当前凭据身份能否保留已保存的密钥。 */
export function emailSecretStored(values: EmailFormValues, saved: PlatformEmailSettings) {
  if (values.provider !== saved.provider) return false
  switch (values.provider) {
    case EmailProvider.SMTP: return !!saved.smtp?.passwordSet && values.smtp.host.trim() === saved.smtp.host && values.smtp.username.trim() === saved.smtp.username
    case EmailProvider.SES: return !!saved.ses?.secretAccessKeySet && values.ses.accessKeyID.trim() === saved.ses.accessKeyID
    case EmailProvider.Mailgun: return !!saved.mailgun?.apiKeySet
    case EmailProvider.Postmark: return !!saved.postmark?.serverTokenSet
    case EmailProvider.Resend: return !!saved.resend?.apiKeySet
  }
}

/** 校验当前方式的字段，启用时要求完整配置。 */
export function createEmailSchema(t: Translator, saved: PlatformEmailSettings) {
  return z.object({
    enabled: z.boolean(), provider: z.enum(EmailProvider), fromAddress: z.string().trim(), fromName: z.string().trim(),
    smtp: z.object({ host: z.string().trim(), port: z.string(), security: z.string(), username: z.string().trim(), password: z.string() }),
    ses: z.object({ region: z.string().trim(), accessKeyID: z.string().trim(), secretAccessKey: z.string().trim() }),
    mailgun: z.object({ domain: z.string().trim(), region: z.string(), apiKey: z.string().trim() }),
    postmark: z.object({ messageStream: z.string().trim(), serverToken: z.string().trim() }),
    resend: z.object({ apiKey: z.string().trim() }),
  }).superRefine((v, context) => {
    const invalid = (path: string[], message = t("deployment.validation.emailSettingInvalid")) => context.addIssue({ code: "custom", path, message })
    const validDomain = (value: string) => value.length <= 253 && value.split(".").every(label => label.length <= 63 && /^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$/.test(label))
    const stored = emailSecretStored(v, saved)
    const secret = (value: string, field: string) => {
      if ((v.enabled && !value && !stored) || value.length > 4096 || /[\r\n]/.test(value)) invalid([v.provider, field])
    }
    if ((v.enabled || v.fromAddress) && !z.email().safeParse(v.fromAddress).success) invalid(["fromAddress"], t("deployment.validation.fromAddressInvalid"))
    if (Array.from(v.fromName).length > 128 || /[\r\n]/.test(v.fromName)) invalid(["fromName"])
    switch (v.provider) {
      case EmailProvider.SMTP: {
        const m = v.smtp
        if ((v.enabled || m.host) && !validDomain(m.host) && !z.ipv6().safeParse(m.host).success) invalid(["smtp", "host"])
        const port = Number(m.port)
        if (!Number.isInteger(port) || port < 1 || port > 65535) invalid(["smtp", "port"], t("deployment.validation.portInvalid"))
        if (!["starttls", "tls", "none"].includes(m.security)) invalid(["smtp", "security"])
        if ((!m.username && m.password) || (v.enabled && m.username && !m.password && !stored)) invalid(["smtp", "password"], t("deployment.validation.credentialsIncomplete"))
        if (m.username.length > 320 || /[\r\n]/.test(m.username)) invalid(["smtp", "username"])
        if (m.password.length > 4096 || /[\r\n]/.test(m.password)) invalid(["smtp", "password"])
        break
      }
      case EmailProvider.SES:
        if ((v.enabled || v.ses.region) && !/^[a-z]{2,8}(?:-[a-z]+)+-[0-9]+$/.test(v.ses.region)) invalid(["ses", "region"])
        if ((v.enabled && !v.ses.accessKeyID) || v.ses.accessKeyID.length > 128 || /[\r\n]/.test(v.ses.accessKeyID)) invalid(["ses", "accessKeyID"])
        secret(v.ses.secretAccessKey, "secretAccessKey")
        break
      case EmailProvider.Mailgun:
        if ((v.enabled || v.mailgun.domain) && !validDomain(v.mailgun.domain)) invalid(["mailgun", "domain"])
        if (!["us", "eu"].includes(v.mailgun.region)) invalid(["mailgun", "region"])
        secret(v.mailgun.apiKey, "apiKey")
        break
      case EmailProvider.Postmark:
        if (v.postmark.messageStream.length > 128 || /[\r\n]/.test(v.postmark.messageStream)) invalid(["postmark", "messageStream"])
        secret(v.postmark.serverToken, "serverToken")
        break
      case EmailProvider.Resend: secret(v.resend.apiKey, "apiKey"); break
    }
  })
}
