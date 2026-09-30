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

/** 创建模型服务供应商表单校验。 */
export function createAIProviderSchema(
  messages: AIModelSchemaMessages & {
    brandInvalid: string
    credentialTypeInvalid: string
    nameRequired: string
    nameTooLong: string
    apiKeyRequired: string
    apiKeyTooLong: string
    apiUrlRequired: string
    apiUrlInvalid: string
    modelsRequired: string
  },
) {
  return z.object({
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
  }).superRefine((values, context) => {
    // 使用密钥的供应商必须填写密钥，无凭据的服务不校验该字段。
    if (
      values.credentialType === AIProviderCredentialType.AIProviderCredentialTypeAPIKey &&
      values.apiKey === ""
    ) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        message: messages.apiKeyRequired,
        path: ["apiKey"],
      })
    }
  })
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

export type AIModelFormValues = z.infer<ReturnType<typeof createAIModelSchema>>
