/** 通知被点击后待打开页面的交接：主界面登记跳转处理，未登记（界面尚未就绪或正在重新挂载）时暂存最近一次的页面，登记后立即交给它。 */

type NotificationNavigator = (path: string) => void

let activeNavigator: NotificationNavigator | null = null
let heldPath: string | null = null

/** 打开通知对应的页面，暂无跳转处理时暂存。 */
export function openNotificationPath(path: string) {
  if (!path) return
  if (activeNavigator) {
    activeNavigator(path)
    return
  }
  heldPath = path
}

/** 登记跳转处理并交出暂存的页面，返回取消登记函数；只取消仍是当前处理的登记。 */
export function registerNotificationNavigator(navigator: NotificationNavigator) {
  activeNavigator = navigator
  if (heldPath !== null) {
    const path = heldPath
    heldPath = null
    navigator(path)
  }
  return () => {
    if (activeNavigator === navigator) activeNavigator = null
  }
}
