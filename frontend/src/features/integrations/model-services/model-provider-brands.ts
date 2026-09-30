/** 模型服务供应商品牌的界面配置。 */
import { AIProviderBrand, type AIProviderBrandId } from "@/api"
import deepseekIcon from "@lobehub/icons-static-svg/icons/deepseek-color.svg?raw"
import alibabaIcon from "@lobehub/icons-static-svg/icons/bailian-color.svg?raw"
import openaiIcon from "@lobehub/icons-static-svg/icons/openai.svg?raw"
import anthropicIcon from "@lobehub/icons-static-svg/icons/anthropic.svg?raw"
import googleIcon from "@lobehub/icons-static-svg/icons/gemini-color.svg?raw"
import moonshotIcon from "@lobehub/icons-static-svg/icons/moonshot.svg?raw"
import zhipuIcon from "@lobehub/icons-static-svg/icons/zhipu-color.svg?raw"
import volcengineIcon from "@lobehub/icons-static-svg/icons/volcengine-color.svg?raw"
import minimaxIcon from "@lobehub/icons-static-svg/icons/minimax-color.svg?raw"
import xaiIcon from "@lobehub/icons-static-svg/icons/xai.svg?raw"
import mistralIcon from "@lobehub/icons-static-svg/icons/mistral-color.svg?raw"
import openrouterIcon from "@lobehub/icons-static-svg/icons/openrouter.svg?raw"
import ollamaIcon from "@lobehub/icons-static-svg/icons/ollama.svg?raw"

type AIProviderBrandConfig = {
  nameKey: `modelServices.brands.${AIProviderBrandId}`
  defaultAPIURL: string
  /** 品牌标志的 SVG 源码，没有公开标志的品牌和通用兼容服务不设置。 */
  icon?: string
  /** 模型目录由服务实例提供，添加模型时读取服务实例。 */
  discoversModels?: boolean
  /** 自建或本机部署的服务可以不配置凭据。 */
  supportsNoCredential?: boolean
}

export const aiProviderBrandOrder: AIProviderBrandId[] = [
  AIProviderBrand.AIProviderBrandDeepSeek,
  AIProviderBrand.AIProviderBrandAlibaba,
  AIProviderBrand.AIProviderBrandOpenAI,
  AIProviderBrand.AIProviderBrandAnthropic,
  AIProviderBrand.AIProviderBrandGoogle,
  AIProviderBrand.AIProviderBrandMoonshot,
  AIProviderBrand.AIProviderBrandZhipu,
  AIProviderBrand.AIProviderBrandVolcengine,
  AIProviderBrand.AIProviderBrandMiniMax,
  AIProviderBrand.AIProviderBrandXAI,
  AIProviderBrand.AIProviderBrandMistral,
  AIProviderBrand.AIProviderBrandOpenRouter,
  AIProviderBrand.AIProviderBrandTypeSafe,
  AIProviderBrand.AIProviderBrandOllama,
  AIProviderBrand.AIProviderBrandOpenAICompatible,
]

export const aiProviderBrandConfigs: Record<
  AIProviderBrandId,
  AIProviderBrandConfig
> = {
  [AIProviderBrand.AIProviderBrandDeepSeek]: {
    nameKey: "modelServices.brands.deepseek",
    icon: deepseekIcon,
    defaultAPIURL: "https://api.deepseek.com",
  },
  [AIProviderBrand.AIProviderBrandAlibaba]: {
    nameKey: "modelServices.brands.alibaba",
    icon: alibabaIcon,
    defaultAPIURL: "https://dashscope.aliyuncs.com",
  },
  [AIProviderBrand.AIProviderBrandOpenAI]: {
    nameKey: "modelServices.brands.openai",
    icon: openaiIcon,
    defaultAPIURL: "https://api.openai.com/v1",
  },
  [AIProviderBrand.AIProviderBrandAnthropic]: {
    nameKey: "modelServices.brands.anthropic",
    icon: anthropicIcon,
    defaultAPIURL: "https://api.anthropic.com",
  },
  [AIProviderBrand.AIProviderBrandGoogle]: {
    nameKey: "modelServices.brands.google",
    icon: googleIcon,
    defaultAPIURL: "https://generativelanguage.googleapis.com",
  },
  [AIProviderBrand.AIProviderBrandMoonshot]: {
    nameKey: "modelServices.brands.moonshot",
    icon: moonshotIcon,
    defaultAPIURL: "https://api.moonshot.cn/v1",
  },
  [AIProviderBrand.AIProviderBrandZhipu]: {
    nameKey: "modelServices.brands.zhipu",
    icon: zhipuIcon,
    defaultAPIURL: "https://open.bigmodel.cn/api/paas/v4",
  },
  [AIProviderBrand.AIProviderBrandVolcengine]: {
    nameKey: "modelServices.brands.volcengine",
    icon: volcengineIcon,
    defaultAPIURL: "https://ark.cn-beijing.volces.com/api/v3",
  },
  [AIProviderBrand.AIProviderBrandMiniMax]: {
    nameKey: "modelServices.brands.minimax",
    icon: minimaxIcon,
    defaultAPIURL: "https://api.minimaxi.com/v1",
  },
  [AIProviderBrand.AIProviderBrandXAI]: {
    nameKey: "modelServices.brands.xai",
    icon: xaiIcon,
    defaultAPIURL: "https://api.x.ai/v1",
  },
  [AIProviderBrand.AIProviderBrandMistral]: {
    nameKey: "modelServices.brands.mistral",
    icon: mistralIcon,
    defaultAPIURL: "https://api.mistral.ai/v1",
  },
  [AIProviderBrand.AIProviderBrandOpenRouter]: {
    nameKey: "modelServices.brands.openrouter",
    icon: openrouterIcon,
    defaultAPIURL: "https://openrouter.ai/api/v1",
    discoversModels: true,
  },
  [AIProviderBrand.AIProviderBrandTypeSafe]: {
    nameKey: "modelServices.brands.typesafe",
    defaultAPIURL: "https://api.typesafe.ai/v1",
  },
  [AIProviderBrand.AIProviderBrandOllama]: {
    nameKey: "modelServices.brands.ollama",
    icon: ollamaIcon,
    defaultAPIURL: "http://localhost:11434",
    discoversModels: true,
    supportsNoCredential: true,
  },
  [AIProviderBrand.AIProviderBrandOpenAICompatible]: {
    nameKey: "modelServices.brands.openai_compatible",
    defaultAPIURL: "",
    discoversModels: true,
    supportsNoCredential: true,
  },
}
