/** 从结构化业务错误中取出字段或总览文案。 */
import i18n from "i18next"

import { isApiError, type ApiError } from "@/api"

/** 按字段顺序读取错误文案，没有字段错误时返回总览消息；服务端处理失败的错误附上错误编号，供用户报告给管理员。 */
export function apiErrorMessage(
  error: ApiError,
  fieldNames: readonly string[] = [],
) {
  for (const name of fieldNames) {
    const message = error.fields[name]
    if (message) {
      return message
    }
  }

  const message = Object.values(error.fields).find(Boolean) ?? error.message
  return error.kind === "failed" && error.traceId
    ? i18n.t("common:errors.withTraceId", { message, traceId: error.traceId })
    : message
}

/** 返回请求失败的提示文案：接口错误按字段顺序取服务端文案，其余提示无法连接服务器。 */
export function requestErrorMessage(error: unknown, fieldNames: readonly string[] = []) {
  return isApiError(error) ? apiErrorMessage(error, fieldNames) : i18n.t("common:errors.network")
}
