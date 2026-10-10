/** 模型服务供应商与模型选项调用。 */
import type { AIModelUsage, AIProviderBrand, AIProviderConnectionInput } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

/** 读取当前工作区满足指定用途的模型。 */
export function listAIModelOptions(usage: AIModelUsage) {
  return ops.listAIModelOptions(usage).then((output) => output.models)
}

/** 读取当前企业的模型服务供应商列表。 */
export const listAIProviders = ops.listAIProviders

/** 读取模型服务供应商详情。 */
export const getAIProvider = ops.getAIProvider

/** 读取指定品牌的预设模型目录。 */
export function listAvailableAIModels(brand: AIProviderBrand) {
  return ops.listAvailableAIModels(brand).then((output) => output.models)
}

/** 读取模型服务实例当前可用的模型。 */
export function discoverAIProviderModels(input: AIProviderConnectionInput) {
  return ops.discoverAIProviderModels(input).then((output) => output.models)
}

/** 测试模型服务供应商草稿配置。 */
export const testAIProviderConnection = ops.testAIProviderConnection

/** 创建模型服务供应商。 */
export const createAIProvider = ops.createAIProvider

/** 修改模型服务供应商。 */
export const updateAIProvider = ops.updateAIProvider

/** 删除模型服务供应商。 */
export const deleteAIProvider = ops.deleteAIProvider
