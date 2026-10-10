/** 请求失败的统一处理：恢复会话入口、记录日志和错误提示。 */
import { useCallback } from "react"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { isApiError } from "@/api"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 请求失败的处理选项：log 为日志动作名，fallback 为非接口错误的提示，fields 为按序读取的字段错误。 */
export type RequestErrorOptions = {
  log?: string
  context?: Record<string, unknown>
  fallback?: string
  fields?: readonly string[]
}

/** 返回请求失败处理函数：会话失效时转入会话入口并返回 true，否则按 log 记录日志、提示错误并返回 false。 */
export function useRequestErrorReporter() {
  const navigate = useNavigate()
  return useCallback(
    (error: unknown, { log, context, fallback, fields }: RequestErrorOptions = {}) => {
      if (recoverSession(error, navigate)) return true
      if (log) console.warn(`${log}失败`, { ...context, error })
      toast.error(fallback && !isApiError(error) ? fallback : requestErrorMessage(error, fields))
      return false
    },
    [navigate],
  )
}
