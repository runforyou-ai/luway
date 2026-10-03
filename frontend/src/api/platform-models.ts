/** 平台模型服务调用：平台供应商、平台模型目录与平台模型调用记录。 */
import {
  CreatePlatformAIModel,
  CreatePlatformAIProvider,
  DeletePlatformAIModel,
  DeletePlatformAIProvider,
  GetPlatformAIModel,
  GetPlatformAIModelCall,
  GetPlatformAIProvider,
  ListPlatformAIModelCalls,
  ListPlatformAIModels,
  ListPlatformAIProviderModels,
  ListPlatformAIProviders,
  UpdatePlatformAIModel,
  UpdatePlatformAIProvider,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  AIModelCallStatus,
  type AIModelCallActor,
  type PlatformAIModel,
  type PlatformAIModelCall,
  type PlatformAIModelCallAttempt,
  type PlatformAIModelCallListInput,
  type PlatformAIModelInput,
  type PlatformAIModelRoute,
  type PlatformAIProvider,
  type PlatformAIProviderInput,
  type PlatformAIProviderSummary,
  type PlatformAIProviderUpdateInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import type {
  AIModelInputModalityId,
  AIModelTypeId,
  AIModelUsageId,
  AIProviderBrandId,
  AIProviderCredentialTypeId,
  AIProviderModelData,
} from "@/api/ai-providers"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type AIModelCallStatusId = Exclude<AIModelCallStatus, AIModelCallStatus.$zero>

export type PlatformAIProviderSummaryData = Omit<PlatformAIProviderSummary, "brand"> & {
  brand: AIProviderBrandId
}

export type PlatformAIProviderData = Omit<PlatformAIProvider, "brand" | "credentialType"> & {
  brand: AIProviderBrandId
  credentialType: AIProviderCredentialTypeId
}

export type PlatformAIModelRouteData = Omit<PlatformAIModelRoute, "providerBrand"> & {
  providerBrand: AIProviderBrandId
}

export type PlatformAIModelData = Omit<
  NonNullArrays<PlatformAIModel>,
  "type" | "inputModalities" | "routes"
> & {
  type: AIModelTypeId
  inputModalities: AIModelInputModalityId[]
  routes: PlatformAIModelRouteData[]
}

export type PlatformAIModelCallData = Omit<PlatformAIModelCall, "usage" | "status" | "actor"> & {
  usage: AIModelUsageId
  status: AIModelCallStatusId
  actor: Exclude<AIModelCallActor, AIModelCallActor.$zero>
}

export type PlatformAIModelCallAttemptData = Omit<PlatformAIModelCallAttempt, "status"> & {
  status: AIModelCallStatusId
}

const listPlatformAIProvidersBound = bind(ListPlatformAIProviders)
const getPlatformAIProviderBound = bind(GetPlatformAIProvider)
const listPlatformAIProviderModelsBound = bind(ListPlatformAIProviderModels)
const createPlatformAIProviderBound = bind(CreatePlatformAIProvider)
const updatePlatformAIProviderBound = bind(UpdatePlatformAIProvider)
const listPlatformAIModelsBound = bind(ListPlatformAIModels)
const getPlatformAIModelBound = bind(GetPlatformAIModel)
const createPlatformAIModelBound = bind(CreatePlatformAIModel)
const updatePlatformAIModelBound = bind(UpdatePlatformAIModel)
const listPlatformAIModelCallsBound = bind(ListPlatformAIModelCalls)
const getPlatformAIModelCallBound = bind(GetPlatformAIModelCall)

/** 读取部署的平台供应商。 */
export function listPlatformAIProviders(signal?: AbortSignal) {
  return listPlatformAIProvidersBound(signal).then(
    (output) => output.providers as PlatformAIProviderSummaryData[],
  )
}

/** 读取平台供应商详情。 */
export function getPlatformAIProvider(providerId: string, signal?: AbortSignal) {
  return getPlatformAIProviderBound(providerId, signal) as Promise<PlatformAIProviderData>
}

/** 读取平台供应商可提供的模型。 */
export function listPlatformAIProviderModels(providerId: string, signal?: AbortSignal) {
  return listPlatformAIProviderModelsBound(providerId, signal).then(
    (output) => output.models as AIProviderModelData[],
  )
}

/** 创建平台供应商。 */
export function createPlatformAIProvider(input: PlatformAIProviderInput) {
  return createPlatformAIProviderBound(input) as Promise<PlatformAIProviderData>
}

/** 修改平台供应商，品牌沿用创建时的值。 */
export function updatePlatformAIProvider(providerId: string, input: PlatformAIProviderUpdateInput) {
  return updatePlatformAIProviderBound(providerId, input) as Promise<PlatformAIProviderData>
}

/** 删除不是任何平台模型来源的平台供应商。 */
export const deletePlatformAIProvider = bind(DeletePlatformAIProvider)

/** 读取部署的平台模型目录。 */
export function listPlatformAIModels(signal?: AbortSignal) {
  return listPlatformAIModelsBound(signal).then((output) => output.models as PlatformAIModelData[])
}

/** 读取平台模型详情。 */
export function getPlatformAIModel(modelId: string, signal?: AbortSignal) {
  return getPlatformAIModelBound(modelId, signal) as Promise<PlatformAIModelData>
}

/** 创建对全部工作区可用的平台模型。 */
export function createPlatformAIModel(input: PlatformAIModelInput) {
  return createPlatformAIModelBound(input) as Promise<PlatformAIModelData>
}

/** 修改平台模型的属性与来源。 */
export function updatePlatformAIModel(modelId: string, input: PlatformAIModelInput) {
  return updatePlatformAIModelBound(modelId, input) as Promise<PlatformAIModelData>
}

/** 删除没有被工作区引用的平台模型。 */
export const deletePlatformAIModel = bind(DeletePlatformAIModel)

/** 读取平台模型调用记录，模型与状态缺省为不限。 */
export function listPlatformAIModelCalls(query: Partial<PlatformAIModelCallListInput>, signal?: AbortSignal) {
  return listPlatformAIModelCallsBound(
    {
      modelId: query.modelId ?? "",
      status: query.status ?? AIModelCallStatus.$zero,
      query: query.query ?? "",
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  ).then((output) => ({ calls: output.calls as PlatformAIModelCallData[], page: output.page }))
}

/** 读取平台模型调用及其上游尝试。 */
export function getPlatformAIModelCall(callId: string, signal?: AbortSignal) {
  return getPlatformAIModelCallBound(callId, signal).then((output) => ({
    call: output.call as PlatformAIModelCallData,
    attempts: output.attempts as PlatformAIModelCallAttemptData[],
  }))
}
