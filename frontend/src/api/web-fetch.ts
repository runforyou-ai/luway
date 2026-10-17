/** 网页读取设置调用。 */
import * as ops from "@/api/generated/operations"

/** 读取当前企业的网页读取设置。 */
export const getWebFetchSettings = ops.getWebFetchSettings

/** 修改当前企业的网页读取设置。 */
export const updateWebFetchSettings = ops.updateWebFetchSettings

/** 用草稿配置读取一次固定页面，验证读取服务可用。 */
export const testWebFetchService = ops.testWebFetchService
