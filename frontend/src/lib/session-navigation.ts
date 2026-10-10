/** 根据应用服务错误恢复会话入口路由。 */
import type { NavigateFunction } from "react-router"

import { ErrorKind, isApiError, sessionPath, SessionState } from "@/api"
import { clearToken, hasToken } from "@/api/client"
import { resourceKeys } from "@/hooks/resource-keys"
import { rememberLoginReturn } from "@/lib/login-return"
import { beginSessionBoundary, resourceClient } from "@/lib/resource-client"

/** 将带有会话状态的错误导航到对应入口，并进入新的登录会话代次；需要登录时记住所在的工作区页面，登录过的会话失效时提示登录已过期；无权限错误重读身份，让导航与入口按最新权限显示。 */
export function recoverSession(
  error: unknown,
  navigate: NavigateFunction,
): boolean {
  if (!isApiError(error)) {
    return false
  }
  const path = sessionPath(error.state)
  if (!path) {
    if (error.kind === ErrorKind.Forbidden) {
      void resourceClient.invalidateQueries({ queryKey: resourceKeys.identity(), exact: true })
    }
    return false
  }
  if (error.state === SessionState.Login) {
    rememberLoginReturn(hasToken())
    clearToken()
  }
  beginSessionBoundary()
  navigate(path, { replace: true })
  return true
}
