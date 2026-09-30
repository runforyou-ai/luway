/** 会话入口页读取当前账号会话。 */
import { useEffect } from "react"
import { useQuery } from "@tanstack/react-query"

import { isApiError, loadAccount, sessionPath, SessionState } from "@/api"
import { clearWebToken } from "@/api/client"
import { noteSessionEstablished } from "@/lib/login-return"
import { resourceKeys } from "@/hooks/resource-keys"

/**
 * 读取当前账号，返回账号资料、读取错误、是否需要重新登录以及应前往的其他会话入口；
 * 需要重新登录时清除本地令牌。读取错误由调用页自行展示，不导航到会话入口。
 */
export function useAccountSession() {
  const account = useQuery({
    queryKey: resourceKeys.account(),
    queryFn: ({ signal }) => loadAccount(signal),
  })
  const sessionState = isApiError(account.error) ? account.error.state : ""
  const signedOut = sessionState === SessionState.SessionStateLogin

  useEffect(() => {
    if (signedOut) clearWebToken()
  }, [signedOut])
  useEffect(() => {
    if (account.data) noteSessionEstablished()
  }, [account.data])

  return {
    account: account.data,
    error: account.error,
    signedOut,
    redirectPath: sessionState && !signedOut ? sessionPath(sessionState) : "",
  }
}
