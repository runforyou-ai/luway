/** 会话已读观察器：可推进已读期间保持同一个 IntersectionObserver，窗口变化时只增量观察消息节点。 */

type ObserverFactory = (
  callback: IntersectionObserverCallback,
  options: IntersectionObserverInit,
) => Pick<IntersectionObserver, "observe" | "unobserve" | "disconnect">

/** 按视口与可读状态维护消息节点的可见性观察。 */
export class MessageReadObserver {
  private readonly callback: IntersectionObserverCallback
  private readonly create: ObserverFactory
  private current: {
    observer: ReturnType<ObserverFactory>
    viewport: ParentNode
    nodes: Set<Element>
  } | null = null

  /** 绑定可见性回调与观察器构造入口。 */
  constructor(
    callback: IntersectionObserverCallback,
    create: ObserverFactory = (next, options) => new IntersectionObserver(next, options),
  ) {
    this.callback = callback
    this.create = create
  }

  /** 不可推进已读或视口替换时释放观察器；可推进时补充观察新节点并释放已移出页面的节点。 */
  sync(viewport: (ParentNode & Element) | null, active: boolean) {
    if (this.current && (!active || this.current.viewport !== viewport)) this.dispose()
    if (!active || !viewport) return
    this.current ??= {
      observer: this.create(this.callback, { root: viewport, threshold: [0, 0.25, 0.5, 1] }),
      viewport,
      nodes: new Set(),
    }
    const { observer, nodes } = this.current
    for (const node of nodes) {
      if (!node.isConnected) {
        observer.unobserve(node)
        nodes.delete(node)
      }
    }
    for (const node of viewport.querySelectorAll("[data-message-id]")) {
      if (!nodes.has(node)) {
        nodes.add(node)
        observer.observe(node)
      }
    }
  }

  /** 停止观察全部节点。 */
  dispose() {
    this.current?.observer.disconnect()
    this.current = null
  }
}
