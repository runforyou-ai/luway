/** 连接链接携带的部署地址的交接：地址在连接成功或用户取消前保持待处理，连接页每次登记接收处理时都会收到它，之后到来的地址直接交给当前处理。 */

type ServerLinkReceiver = (serverURL: string) => void

let activeReceiver: ServerLinkReceiver | null = null
let pendingServerURL: string | null = null

/** 记录连接链接携带的部署地址，连接页已挂载时立即交给它。 */
export function offerServerLink(serverURL: string) {
  pendingServerURL = serverURL
  activeReceiver?.(serverURL)
}

/** 是否有待处理的连接链接。 */
export function hasPendingServerLink() {
  return pendingServerURL !== null
}

/** 连接成功或用户取消后清除待处理的连接链接。 */
export function clearPendingServerLink() {
  pendingServerURL = null
}

/** 登记接收处理并交出待处理的地址，返回取消登记函数；只取消仍是当前处理的登记。 */
export function registerServerLinkReceiver(receiver: ServerLinkReceiver) {
  activeReceiver = receiver
  if (pendingServerURL !== null) receiver(pendingServerURL)
  return () => {
    if (activeReceiver === receiver) activeReceiver = null
  }
}
