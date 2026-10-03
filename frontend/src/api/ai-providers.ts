/** 模型服务供应商与模型选项调用。 */
import {
  CreateAIProvider,
  DeleteAIProvider,
  DiscoverAIProviderModels,
  GetAIProvider,
  ListAIModelOptions,
  ListAIProviders,
  ListAvailableAIModels,
  TestAIProviderConnection,
  UpdateAIProvider,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  AIModelInputModality,
  AIModelType,
  AIModelUsage,
  AIProviderBrand,
  AIProviderCredentialType,
  type AIModelOption,
  type AIProvider,
  type AIProviderConnectionInput,
  type AIProviderInput,
  type AIProviderList,
  type AIProviderModel,
  type AIProviderModelSummary,
  type AIProviderSummary,
  type AIProviderUpdateInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type AIProviderBrandId = Exclude<AIProviderBrand, AIProviderBrand.$zero>

export type AIProviderCredentialTypeId = Exclude<
  AIProviderCredentialType,
  AIProviderCredentialType.$zero
>

export type AIModelTypeId = Exclude<AIModelType, AIModelType.$zero>

export type AIModelInputModalityId = Exclude<
  AIModelInputModality,
  AIModelInputModality.$zero
>

export type AIProviderModelData = Omit<
  NonNullArrays<AIProviderModel>,
  "type" | "inputModalities"
> & {
  type: AIModelTypeId
  inputModalities: AIModelInputModalityId[]
}

type AIProviderData = Omit<
  NonNullArrays<AIProvider>,
  "brand" | "credentialType" | "models"
> & {
  brand: AIProviderBrandId
  credentialType: AIProviderCredentialTypeId
  models: AIProviderModelData[]
}

type AIProviderModelSummaryData = Omit<
  NonNullArrays<AIProviderModelSummary>,
  "type"
> & {
  type: AIModelTypeId
}

export type AIProviderSummaryData = Omit<
  NonNullArrays<AIProviderSummary>,
  "brand" | "models"
> & {
  brand: AIProviderBrandId
  models: AIProviderModelSummaryData[]
}

export type AIModelUsageId = Exclude<AIModelUsage, AIModelUsage.$zero>

export type AIModelOptionData = Omit<
  NonNullArrays<AIModelOption>,
  "type" | "inputModalities" | "provider"
> & {
  type: AIModelTypeId
  inputModalities: AIModelInputModalityId[]
  provider: { id: string; name: string; brand: AIProviderBrandId } | null
}

type AIProviderListData = Omit<
  NonNullArrays<AIProviderList>,
  "providers"
> & {
  providers: AIProviderSummaryData[]
}

const listAIModelOptionsBound = bind(ListAIModelOptions)
const listAIProvidersBound = bind(ListAIProviders)
const getAIProviderBound = bind(GetAIProvider)
const listAvailableAIModelsBound = bind(ListAvailableAIModels)
const discoverAIProviderModelsBound = bind(DiscoverAIProviderModels)
const createAIProviderBound = bind(CreateAIProvider)
const updateAIProviderBound = bind(UpdateAIProvider)

/** 读取当前工作区满足指定用途的模型。 */
export function listAIModelOptions(usage: AIModelUsageId) {
  return listAIModelOptionsBound(usage).then(
    (output) => output.models as AIModelOptionData[],
  )
}

/** 读取当前企业的模型服务供应商列表。 */
export function listAIProviders() {
  return listAIProvidersBound() as Promise<AIProviderListData>
}

/** 读取模型服务供应商详情。 */
export function getAIProvider(providerId: string) {
  return getAIProviderBound(providerId) as Promise<AIProviderData>
}

/** 读取指定品牌的预设模型目录。 */
export function listAvailableAIModels(brand: AIProviderBrand) {
  return listAvailableAIModelsBound(brand).then(
    (output) => output.models as AIProviderModelData[],
  )
}

/** 读取模型服务实例当前可用的模型。 */
export function discoverAIProviderModels(input: AIProviderConnectionInput) {
  return discoverAIProviderModelsBound(input).then(
    (output) => output.models as AIProviderModelData[],
  )
}

/** 测试模型服务供应商草稿配置。 */
export const testAIProviderConnection = bind(TestAIProviderConnection)

/** 创建模型服务供应商。 */
export function createAIProvider(input: AIProviderInput) {
  return createAIProviderBound(input) as Promise<AIProviderData>
}

/** 修改模型服务供应商。 */
export function updateAIProvider(providerId: string, input: AIProviderUpdateInput) {
  return updateAIProviderBound(providerId, input) as Promise<AIProviderData>
}

/** 删除模型服务供应商。 */
export const deleteAIProvider = bind(DeleteAIProvider)
