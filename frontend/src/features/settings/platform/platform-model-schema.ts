/** 平台模型表单校验规则与表单值转换。 */
import { z } from "zod"

import { AIModelInputModality, AIModelType, type PlatformAIModelData } from "@/api"
import { formatTokenCount } from "@/features/integrations/model-services/model-provider-model-values"
import { parseTokenCount, type AIModelSchemaMessages } from "@/features/integrations/model-services/model-provider-schema"
import { requiredWailsEnum } from "@/lib/wails-enum"

/** 平台模型表单的校验提示：模型属性沿用模型目录的提示，另有来源与价格的提示。 */
export type PlatformModelSchemaMessages = AIModelSchemaMessages & {
  providerRequired: string
  routesRequired: string
  routeDuplicate: string
  priceInvalid: string
}

/** 平台模型单项积分价格的上限。 */
const maxCreditPrice = 1_000_000_000

/** 各模型类型需要填写的价格项：对话模型按输入与输出 Token 计价，向量与重排模型按输入 Token 计价，判断模型只按次计价。 */
export const priceFieldsByType: Record<AIModelType, readonly ("inputPrice" | "outputPrice" | "requestPrice")[]> = {
  [AIModelType.$zero]: [],
  [AIModelType.AIModelTypeChat]: ["inputPrice", "outputPrice", "requestPrice"],
  [AIModelType.AIModelTypeEmbedding]: ["inputPrice", "requestPrice"],
  [AIModelType.AIModelTypeRerank]: ["inputPrice", "requestPrice"],
  [AIModelType.AIModelTypeDecision]: ["requestPrice"],
}

/** 把价格输入解析为积分，不是 0 到上限之间的整数时返回 null。 */
export function parseCreditPrice(value: string) {
  const trimmed = value.trim()
  if (!/^\d+$/.test(trimmed)) return null
  const price = Number(trimmed)
  return price <= maxCreditPrice ? price : null
}

/** 创建平台模型表单校验：属性规则与模型目录一致，来源至少一个且同一供应商的模型标识不重复。 */
export function createPlatformModelSchema(messages: PlatformModelSchemaMessages) {
  return z
    .object({
      name: z.string().trim().min(1, messages.modelNameRequired).max(200, messages.modelNameTooLong),
      type: requiredWailsEnum(AIModelType, messages.modelTypeInvalid),
      inputModalities: z
        .array(requiredWailsEnum(AIModelInputModality, messages.inputModalityInvalid))
        .min(1, messages.inputModalitiesRequired),
      contextWindow: z
        .string()
        .trim()
        .refine((value) => parseTokenCount(value) !== null, messages.contextWindowInvalid),
      maxOutputTokens: z.string().trim(),
      priced: z.boolean(),
      inputPrice: z.string(),
      outputPrice: z.string(),
      requestPrice: z.string(),
      routes: z
        .array(
          z.object({
            // 表单内稳定标识，新增来源保存后据此回填服务端编号。
            key: z.string(),
            // 已保存来源的编号，新增来源为空。
            id: z.string(),
            providerId: z.string().min(1, messages.providerRequired),
            identifier: z
              .string()
              .trim()
              .min(1, messages.modelIdentifierRequired)
              .max(200, messages.modelIdentifierTooLong),
            enabled: z.boolean(),
          }),
        )
        .min(1, messages.routesRequired)
        .superRefine((routes, context) => {
          const targets = new Set<string>()
          routes.forEach((route, index) => {
            const target = `${route.providerId}\n${route.identifier.trim()}`
            if (targets.has(target)) {
              context.addIssue({ code: z.ZodIssueCode.custom, message: messages.routeDuplicate, path: [index, "identifier"] })
            }
            targets.add(target)
          })
        }),
    })
    .superRefine((model, context) => {
      if (model.type === AIModelType.AIModelTypeChat && parseTokenCount(model.maxOutputTokens) === null) {
        context.addIssue({ code: z.ZodIssueCode.custom, message: messages.maxOutputTokensInvalid, path: ["maxOutputTokens"] })
      }
      // 定价时校验当前模型类型需要填写的价格项。
      if (!model.priced) return
      for (const field of priceFieldsByType[model.type]) {
        if (parseCreditPrice(model[field]) === null) {
          context.addIssue({ code: z.ZodIssueCode.custom, message: messages.priceInvalid, path: [field] })
        }
      }
    })
}

export type PlatformModelFormValues = z.infer<ReturnType<typeof createPlatformModelSchema>>

/** 把表单中的价格转换为积分价格，未定价时为空，当前模型类型不需要的价格项为 0。 */
export function platformModelPrice(values: PlatformModelFormValues) {
  if (!values.priced) return null
  const fields = priceFieldsByType[values.type]
  const value = (field: (typeof fields)[number]) => (fields.includes(field) ? (parseCreditPrice(values[field]) ?? 0) : 0)
  return { input: value("inputPrice"), output: value("outputPrice"), request: value("requestPrice") }
}

/** 把平台模型契约转换为表单值，已保存来源以编号作为表单内标识。 */
export function platformModelFormValue(model: PlatformAIModelData): PlatformModelFormValues {
  return {
    name: model.name,
    type: model.type,
    inputModalities: model.inputModalities,
    contextWindow: model.contextWindow > 0 ? formatTokenCount(model.contextWindow) : "",
    maxOutputTokens: model.maxOutputTokens > 0 ? formatTokenCount(model.maxOutputTokens) : "",
    priced: model.price !== null,
    inputPrice: model.price ? String(model.price.input) : "",
    outputPrice: model.price ? String(model.price.output) : "",
    requestPrice: model.price ? String(model.price.request) : "",
    routes: model.routes.map((route) => ({
      key: route.id,
      id: route.id,
      providerId: route.providerId,
      identifier: route.identifier,
      enabled: route.enabled,
    })),
  }
}
