/** 模型服务供应商表单校验规则。 */
import { isHTTPEndpoint } from "@/lib/http-url"
import { z } from "zod"

import {
  AIModelInputModality,
  AIModelType,
  AIProviderBrand,
  AIProviderCredentialType,
} from "@/api"
import { requiredWailsEnum } from "@/lib/wails-enum"

export type AIModelSchemaMessages = {
  modelIdentifierRequired: string
  modelIdentifierTooLong: string
  modelIdentifierDuplicate: string
  modelNameRequired: string
  modelNameTooLong: string
  modelTypeInvalid: string
  inputModalityInvalid: string
  inputModalitiesRequired: string
  contextWindowInvalid: string
  maxOutputTokensInvalid: string
}

/** 创建单个模型的校验，模型标识不能与 takenIdentifiers 中的标识重复。 */
export function createAIModelSchema(
  messages: AIModelSchemaMessages,
  takenIdentifiers: ReadonlySet<string> = new Set(),
) {
  return z
    .object({
      // 表单内稳定标识，新增模型保存后据此回填服务端编号。
      key: z.string(),
      // 已保存模型的编号，新增模型为空。
      id: z.string(),
      identifier: z
        .string()
        .trim()
        .min(1, messages.modelIdentifierRequired)
        .max(200, messages.modelIdentifierTooLong)
        .refine(
          (identifier) => !takenIdentifiers.has(identifier),
          messages.modelIdentifierDuplicate,
        ),
      name: z
        .string()
        .trim()
        .min(1, messages.modelNameRequired)
        .max(200, messages.modelNameTooLong),
      type: requiredWailsEnum(AIModelType, messages.modelTypeInvalid),
      inputModalities: z
        .array(
          requiredWailsEnum(AIModelInputModality, messages.inputModalityInvalid),
        )
        .min(1, messages.inputModalitiesRequired),
      contextWindow: z
        .string()
        .trim()
        .refine(
          (value) => parseTokenCount(value) !== null,
          messages.contextWindowInvalid,
        ),
      maxOutputTokens: z.string().trim(),
    })
    .superRefine((model, context) => {
      if (
        model.type === AIModelType.AIModelTypeChat &&
        parseTokenCount(model.maxOutputTokens) === null
      ) {
        context.addIssue({
          code: z.ZodIssueCode.custom,
          message: messages.maxOutputTokensInvalid,
          path: ["maxOutputTokens"],
        })
      }
    })
}

/** 供应商品牌、名称与连接配置字段的校验提示。 */
export type AIProviderConnectionSchemaMessages = {
  brandInvalid: string
  credentialTypeInvalid: string
  nameRequired: string
  nameTooLong: string
  apiKeyRequired: string
  apiKeyTooLong: string
  apiUrlRequired: string
  apiUrlInvalid: string
}

/** 返回供应商品牌、名称与连接配置字段的校验。 */
function aiProviderConnectionShape(messages: AIProviderConnectionSchemaMessages) {
  return {
    brand: requiredWailsEnum(AIProviderBrand, messages.brandInvalid),
    credentialType: requiredWailsEnum(
      AIProviderCredentialType,
      messages.credentialTypeInvalid,
    ),
    name: z
      .string()
      .trim()
      .min(1, messages.nameRequired)
      .max(100, messages.nameTooLong),
    apiKey: z.string().trim().max(2048, messages.apiKeyTooLong),
    apiUrl: z
      .string()
      .trim()
      .min(1, messages.apiUrlRequired)
      .refine(isHTTPEndpoint, messages.apiUrlInvalid),
  }
}

/** 使用密钥的供应商必须填写密钥，无凭据的服务不校验该字段。 */
function requireAPIKey(
  values: { credentialType: string; apiKey: string },
  context: z.RefinementCtx,
  message: string,
) {
  if (
    values.credentialType === AIProviderCredentialType.AIProviderCredentialTypeAPIKey &&
    values.apiKey === ""
  ) {
    context.addIssue({ code: z.ZodIssueCode.custom, message, path: ["apiKey"] })
  }
}

/** 创建只含品牌、名称与连接配置的供应商表单校验。 */
export function createAIProviderConnectionSchema(messages: AIProviderConnectionSchemaMessages) {
  return z
    .object(aiProviderConnectionShape(messages))
    .superRefine((values, context) => requireAPIKey(values, context, messages.apiKeyRequired))
}

/** 创建模型服务供应商表单校验。 */
export function createAIProviderSchema(
  messages: AIModelSchemaMessages &
    AIProviderConnectionSchemaMessages & { modelsRequired: string },
) {
  return z
    .object({
      ...aiProviderConnectionShape(messages),
      models: z
        .array(createAIModelSchema(messages))
        .min(1, messages.modelsRequired)
        .superRefine((models, context) => {
          const identifiers = new Set<string>()
          models.forEach((model, index) => {
            if (identifiers.has(model.identifier)) {
              context.addIssue({
                code: z.ZodIssueCode.custom,
                message: messages.modelIdentifierDuplicate,
                path: [index, "identifier"],
              })
            }
            identifiers.add(model.identifier)
          })
        }),
    })
    .superRefine((values, context) => requireAPIKey(values, context, messages.apiKeyRequired))
}

/** 把正整数或 K、M 紧凑值转换为 Token 数。 */
export function parseTokenCount(value: string) {
  const matched = value
    .trim()
    .toUpperCase()
    .match(/^(\d+(?:\.\d+)?)([KM]?)$/)
  if (!matched) return null
  const amount = Number(matched[1])
  const multiplier =
    matched[2] === "M" ? 1_048_576 : matched[2] === "K" ? 1024 : 1
  const tokens = amount * multiplier
  return Number.isSafeInteger(tokens) && tokens > 0 ? tokens : null
}

export type AIProviderFormValues = z.infer<
  ReturnType<typeof createAIProviderSchema>
>

export type AIProviderConnectionFormValues = z.infer<
  ReturnType<typeof createAIProviderConnectionSchema>
>

export type AIModelFormValues = z.infer<ReturnType<typeof createAIModelSchema>>
