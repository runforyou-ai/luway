/** 模型校验的本地化提示，工作区模型服务与平台模型共用。 */
import { useMemo } from "react"
import { useTranslation } from "react-i18next"

import type { AIModelSchemaMessages } from "@/components/model-provider/model-provider-schema"

/** 返回单个模型校验使用的本地化提示。 */
export function useAIModelSchemaMessages(): AIModelSchemaMessages {
  const { t } = useTranslation("integrations")
  return useMemo(
    () => ({
      modelIdentifierDuplicate: t("modelServices.validation.modelIdentifierDuplicate"),
      inputModalitiesRequired: t("modelServices.validation.inputModalitiesRequired"),
      contextWindowInvalid: t("modelServices.validation.contextWindowInvalid"),
      maxOutputTokensInvalid: t("modelServices.validation.maxOutputTokensInvalid"),
    }),
    [t],
  )
}
