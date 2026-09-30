/** 根据应用服务错误恢复会话入口路由。 */
import type { NavigateFunction } from "react-router"

import { isApiError, sessionPath, SessionState } from "@/api"
import { clearWebToken, hasWebToken } from "@/api/client"
import { rememberLoginReturn } from "@/lib/login-return"
import { beginSessionBoundary } from "@/lib/resource-client"

/** 将带有会话状态的错误导航到对应入口，并进入新的登录会话代次；需要登录时记住所在的工作区页面，登录过的会话失效时提示登录已过期。 */
export function recoverSession(
  error: unknown,
  navigate: NavigateFunction,
): boolean {
  if (!isApiError(error)) {
    return false
  }
  const path = sessionPath(error.state)
  if (!path) {
    return false
  }
  if (error.state === SessionState.SessionStateLogin) {
    rememberLoginReturn(hasWebToken())
    clearWebToken()
  }
  beginSessionBoundary()
  navigate(path, { replace: true })
  return true
}
