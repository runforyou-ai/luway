/** 未登录时打开的邀请：登录或注册完成后回到邀请页。 */

const pendingInvitationStorageKey = "app.pendingInvitation"

/** 返回邀请落地页地址。 */
export function invitationPath(token: string) {
  return `/invitations/${encodeURIComponent(token)}`
}

/** 记住当前标签页中待处理的邀请令牌。 */
export function rememberPendingInvitation(token: string) {
  try {
    window.sessionStorage.setItem(pendingInvitationStorageKey, token)
  } catch {
    // 会话存储不可用时登录后需要重新打开邀请链接。
  }
}

/** 取出并清除待处理的邀请令牌。 */
export function takePendingInvitation() {
  try {
    const token = window.sessionStorage.getItem(pendingInvitationStorageKey)
    window.sessionStorage.removeItem(pendingInvitationStorageKey)
    return token
  } catch {
    return null
  }
}

/** 清除待处理的邀请令牌。 */
export function clearPendingInvitation() {
  try {
    window.sessionStorage.removeItem(pendingInvitationStorageKey)
  } catch {
    // 会话存储不可用时没有需要清除的内容。
  }
}
