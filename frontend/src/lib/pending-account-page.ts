/** 未登录时打开的账号级页面（邀请、渠道绑定）：登录或注册完成后回到该页面。 */

const pendingAccountPageStorageKey = "app.pendingAccountPage"

/** 返回邀请落地页地址。 */
export function invitationPath(token: string) {
  return `/invitations/${encodeURIComponent(token)}`
}

/** 返回渠道绑定确认页地址。 */
export function channelBindingPath(token: string) {
  return `/channel-bindings/${encodeURIComponent(token)}`
}

/** 记住当前标签页中待返回的账号级页面地址。 */
export function rememberPendingAccountPage(path: string) {
  try {
    window.sessionStorage.setItem(pendingAccountPageStorageKey, path)
  } catch {
    // 会话存储不可用时登录后需要重新打开原链接。
  }
}

/** 取出并清除待返回的账号级页面地址。 */
export function takePendingAccountPage() {
  try {
    const path = window.sessionStorage.getItem(pendingAccountPageStorageKey)
    window.sessionStorage.removeItem(pendingAccountPageStorageKey)
    return path
  } catch {
    return null
  }
}

/** 清除待返回的账号级页面地址。 */
export function clearPendingAccountPage() {
  try {
    window.sessionStorage.removeItem(pendingAccountPageStorageKey)
  } catch {
    // 会话存储不可用时没有需要清除的内容。
  }
}
