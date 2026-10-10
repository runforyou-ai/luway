/** 登录前后的交接：需要重新登录时记住要回到的工作区页面，登录过的会话失效时记住过期提示，登录完成后由工作区入口和登录页取出。 */
import { workspaceSlugFromHash } from "./workspace-route.ts"

const pendingReturnPathStorageKey = "app.pendingReturnPath"
const sessionExpiredStorageKey = "app.sessionExpired"

/** 记住登录完成后要打开的工作区页面，非工作区地址不记录。 */
export function rememberPendingReturnPath(path: string) {
  if (!workspaceSlugFromHash(path)) return
  try {
    window.sessionStorage.setItem(pendingReturnPathStorageKey, path)
  } catch {
    // 会话存储不可用时登录后停在默认工作区。
  }
}

/** 取出并清除登录完成后要打开的工作区页面。 */
export function takePendingReturnPath() {
  try {
    const path = window.sessionStorage.getItem(pendingReturnPathStorageKey)
    window.sessionStorage.removeItem(pendingReturnPathStorageKey)
    return path
  } catch {
    return null
  }
}

// 本页是否已经建立过登录会话。
let sessionEstablished = false

/** 记录本页已建立登录会话，之后遇到登录失效时提示登录已过期。 */
export function noteSessionEstablished() {
  sessionEstablished = true
}

/** 需要登录时记住当前所在的工作区页面；本页建立过登录会话或 hadSession 为 true 时让登录页提示登录已过期。 */
export function rememberLoginReturn(hadSession: boolean) {
  rememberPendingReturnPath(window.location.hash.replace(/^#/, ""))
  if (hadSession || sessionEstablished) markSessionExpired()
}

/** 让登录页提示登录已过期，本页回到未建立会话的状态。 */
export function markSessionExpired() {
  sessionEstablished = false
  try {
    window.sessionStorage.setItem(sessionExpiredStorageKey, "1")
  } catch {
    // 会话存储不可用时登录页不提示过期原因。
  }
}

/** 返回登录页是否需要提示登录已过期。 */
export function sessionExpiredNoticePending() {
  try {
    return window.sessionStorage.getItem(sessionExpiredStorageKey) === "1"
  } catch {
    return false
  }
}

/** 清除登录过期提示标记。 */
export function clearSessionExpiredNotice() {
  try {
    window.sessionStorage.removeItem(sessionExpiredStorageKey)
  } catch {
    // 会话存储不可用时没有需要清除的标记。
  }
}
