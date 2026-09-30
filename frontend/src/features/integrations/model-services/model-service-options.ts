/** 模型类型和输入模态的界面配置。 */
import {
  AIModelInputModality,
  AIModelType,
  type AIModelInputModalityId,
  type AIModelTypeId,
} from "@/api"

export const modelTypeOrder: AIModelTypeId[] = [
  AIModelType.AIModelTypeChat,
  AIModelType.AIModelTypeEmbedding,
  AIModelType.AIModelTypeRerank,
  AIModelType.AIModelTypeDecision,
]

export const modelTypeNameKeys: Record<
  AIModelTypeId,
  `modelServices.models.types.${"chat" | "embedding" | "rerank" | "decision"}`
> = {
  [AIModelType.AIModelTypeChat]: "modelServices.models.types.chat",
  [AIModelType.AIModelTypeEmbedding]: "modelServices.models.types.embedding",
  [AIModelType.AIModelTypeRerank]: "modelServices.models.types.rerank",
  [AIModelType.AIModelTypeDecision]: "modelServices.models.types.decision",
}

export const modelInputModalityOrder: AIModelInputModalityId[] = [
  AIModelInputModality.AIModelInputModalityText,
  AIModelInputModality.AIModelInputModalityImage,
  AIModelInputModality.AIModelInputModalityAudio,
  AIModelInputModality.AIModelInputModalityVideo,
]

export const modelInputModalityNameKeys: Record<
  AIModelInputModalityId,
  `modelServices.models.modalities.${"text" | "image" | "audio" | "video"}`
> = {
  [AIModelInputModality.AIModelInputModalityText]:
    "modelServices.models.modalities.text",
  [AIModelInputModality.AIModelInputModalityImage]:
    "modelServices.models.modalities.image",
  [AIModelInputModality.AIModelInputModalityAudio]:
    "modelServices.models.modalities.audio",
  [AIModelInputModality.AIModelInputModalityVideo]:
    "modelServices.models.modalities.video",
}
