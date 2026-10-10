/** 模型类型和输入模态的界面配置。 */
import { AIModelInputModality, AIModelType } from "@/api"

/** 模型类型的展示顺序。 */
export const modelTypeOrder: AIModelType[] = [
  AIModelType.Chat,
  AIModelType.Embedding,
  AIModelType.Rerank,
  AIModelType.Decision,
]

/** 模型类型对应的名称文案键。 */
export const modelTypeNameKeys: Record<
  AIModelType,
  `modelServices.models.types.${"chat" | "embedding" | "rerank" | "decision"}`
> = {
  [AIModelType.Chat]: "modelServices.models.types.chat",
  [AIModelType.Embedding]: "modelServices.models.types.embedding",
  [AIModelType.Rerank]: "modelServices.models.types.rerank",
  [AIModelType.Decision]: "modelServices.models.types.decision",
}

/** 输入模态的展示顺序。 */
export const modelInputModalityOrder: AIModelInputModality[] = [
  AIModelInputModality.Text,
  AIModelInputModality.Image,
  AIModelInputModality.Audio,
  AIModelInputModality.Video,
]

/** 输入模态对应的名称文案键。 */
export const modelInputModalityNameKeys: Record<
  AIModelInputModality,
  `modelServices.models.modalities.${"text" | "image" | "audio" | "video"}`
> = {
  [AIModelInputModality.Text]:
    "modelServices.models.modalities.text",
  [AIModelInputModality.Image]:
    "modelServices.models.modalities.image",
  [AIModelInputModality.Audio]:
    "modelServices.models.modalities.audio",
  [AIModelInputModality.Video]:
    "modelServices.models.modalities.video",
}
