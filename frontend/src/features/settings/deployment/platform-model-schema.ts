/** 平台模型表单校验规则与表单值转换。 */
import { z } from "zod"

import { AIModelInputModality, AIModelType, type PlatformAIModelData } from "@/api"
import { formatTokenCount } from "@/features/integrations/model-services/model-provider-model-values"
import { parseTokenCount, type AIModelSchemaMessages } from "@/features/integrations/model-services/model-provider-schema"
import { requiredWailsEnum } from "@/lib/wails-enum"

/** 平台模型表单的校验提示：模型属性沿用模型目录的提示，另有来源的提示。 */
export type PlatformModelSchemaMessages = AIModelSchemaMessages & {
  providerRequired: string
  routesRequired: string
  routeDuplicate: string
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
    })
}

export type PlatformModelFormValues = z.infer<ReturnType<typeof createPlatformModelSchema>>

/** 把平台模型契约转换为表单值，已保存来源以编号作为表单内标识。 */
export function platformModelFormValue(model: PlatformAIModelData): PlatformModelFormValues {
  return {
    name: model.name,
    type: model.type,
    inputModalities: model.inputModalities,
    contextWindow: model.contextWindow > 0 ? formatTokenCount(model.contextWindow) : "",
    maxOutputTokens: model.maxOutputTokens > 0 ? formatTokenCount(model.maxOutputTokens) : "",
    routes: model.routes.map((route) => ({
      key: route.id,
      id: route.id,
      providerId: route.providerId,
      identifier: route.identifier,
      enabled: route.enabled,
    })),
  }
}
