/** 联网搜索设置调用。 */
import * as ops from "@/api/generated/operations"

/** 读取当前企业的联网搜索设置。 */
export const getWebSearchSettings = ops.getWebSearchSettings

/** 修改当前企业的联网搜索设置，搜索服务为空表示关闭联网搜索。 */
export const updateWebSearchSettings = ops.updateWebSearchSettings

/** 用草稿配置执行一次搜索，验证搜索服务可用。 */
export const testWebSearchService = ops.testWebSearchService
