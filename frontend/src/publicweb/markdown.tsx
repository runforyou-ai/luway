/** 为网站聊天窗口 消息正文提供共享 React 渲染和生命周期接口。 */
import { createRoot, type Root } from "react-dom/client"
import { flushSync } from "react-dom"
import { MessageMarkdown } from "../components/message-markdown"
import type { OrganizationIdentityType } from "../api"

const roots = new Map<HTMLElement, Root>()

/** 同步提交待定位的历史正文，使滚动测量包含 Markdown 的实际布局。 */
export function renderBatch(renderMessages: () => void) {
  flushSync(renderMessages)
}

/** 在稳定的正文节点上更新完整消息或流式原文。 */
export function render(container: HTMLElement, body: string, senderIdentityType: OrganizationIdentityType | null, streaming = false) {
  if (senderIdentityType !== "agent") {
    unmount(container)
    container.textContent = body
    return
  }
  let root = roots.get(container)
  if (!root) {
    root = createRoot(container)
    roots.set(container, root)
  }
  root.render(<MessageMarkdown locale={document.documentElement.lang} streaming={streaming}>{body}</MessageMarkdown>)
}

/** 在消息节点被移除前释放它和后代正文的 React 资源。 */
export function unmount(container: Node) {
  for (const [element, root] of roots) {
    if (container === element || container.contains(element)) {
      root.unmount()
      roots.delete(element)
    }
  }
}
