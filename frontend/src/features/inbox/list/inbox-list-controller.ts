/** 串行调度收件箱窗口读取，只保存列表顺序、查询边界和浏览状态。 */
import type { InboxConversation, InboxQuery, InboxWindow } from "@/api"
import type { loadInbox, readInboxConversations } from "@/api"

export type InboxListOperation = "initial" | "before" | "after" | "poll" | "refresh"
export type InboxListAnchor = {
  id: string
  cursor: string
  width: number
  height: number
  neighbors: { id: string; offset: number }[]
}
type ListPosition = Pick<InboxConversation, "id" | "positionCursor" | "lastActivityAt">
type RowResults = Awaited<ReturnType<typeof readInboxConversations>>
type Page = Awaited<ReturnType<typeof loadInbox>>
type Window = Omit<InboxWindow, "conversations"> & { conversations: InboxConversation[] }

export type InboxListState = {
  ids: string[]
  positions: ListPosition[]
  rowIds: string[]
  unavailableIds: string[]
  startCursor: string
  endCursor: string
  hasBefore: boolean
  hasAfter: boolean
  pinOrderVersion: string
  status: "initial" | "ready" | "loadingMore" | "refreshing"
  operation: InboxListOperation | null
  error: InboxListOperation | null
  revision: number
}

export type InboxListBookmark = { state: InboxListState; anchor: InboxListAnchor | null }

/** bookmark 与 cached 恢复上次浏览窗口，locateId 指定进入列表时定位的会话，region 表示按置顶顺序整区读取。 */
type InboxListControllerOptions = { bookmark?: InboxListBookmark; cached?: boolean; locateId?: string | null; region?: boolean }

export type InboxListPorts = {
  page: (cursor?: string, beforeCursor?: string) => Promise<Page>
  window: (startCursor: string, endCursor: string) => Promise<Window>
  context: (anchor: InboxListAnchor) => Promise<Window>
  rows: (ids: string[]) => Promise<RowResults>
  capture: () => InboxListAnchor | null
  atTop: () => boolean
  interacting: () => boolean
  restore: (anchor: InboxListAnchor | null, moved: Set<string>, top: boolean) => void
  unavailable: (ids: string[]) => void
}

/** 管理页面查询的读取队列，查询切换后丢弃旧结果。 */
export class InboxListController {
  private state: InboxListState = {
    ids: [], positions: [], rowIds: [], unavailableIds: [], startCursor: "", endCursor: "",
    hasBefore: false, hasAfter: false,
    pinOrderVersion: "", status: "initial", operation: null, error: null, revision: 0,
  }
  private listeners = new Set<() => void>()
  private queue: InboxListOperation[] = []
  private running = false
  private completion = Promise.resolve()
  private generation = 0
  private deferred: InboxListState | null = null
  private ownWritePending = false
  private returnAnchor: InboxListAnchor | null = null
  private ports: InboxListPorts
  private query: InboxQuery
  private region: boolean

  /** 绑定当前页面的读取和视口适配器。 */
  constructor(ports: InboxListPorts, query: InboxQuery, { bookmark, cached = false, locateId = null, region = false }: InboxListControllerOptions = {}) {
    this.ports = ports
    this.query = query
    this.region = region
    // 定位锚点没有原位置，按邻居补偿把该会话对齐到窗口顶部。
    this.returnAnchor = bookmark?.anchor ?? (locateId ? { id: locateId, cursor: "", width: 0, height: 0, neighbors: [{ id: locateId, offset: 0 }] } : null)
    if (bookmark && cached) {
      this.state = { ...bookmark.state, operation: null, error: null, status: "ready" }
      this.ports.restore(this.returnAnchor, new Set(), !this.returnAnchor)
    }
  }

  /** 保存窗口元数据与原邻域，摘要继续由 Query 缓存持有。 */
  remember(): InboxListBookmark {
    return { state: this.state, anchor: this.ports.capture() ?? this.returnAnchor }
  }

  /** 交互结束后应用最后一份权威顺序，使用当时可见的原邻居保位。 */
  settle = () => {
    if (!this.deferred || this.ports.interacting()) return
    const next = this.deferred
    this.deferred = null
    this.applyWindow(next, false)
  }

  /** 以原后继、前驱替代已移动的锚点并保持阅读位置。 */
  private applyWindow(next: InboxListState, initial: boolean) {
    const positions = new Map(next.positions.map((row) => [row.id, row]))
    // 置顶区以前驱变化识别被移动的行，普通区以活动时间变化识别上浮的行。
    const predecessors = new Map(next.positions.map((row, index) => [row.id, next.positions[index - 1]?.id]))
    const moved = new Set(this.state.positions.filter((row, index) => this.region
      ? predecessors.get(row.id) !== this.state.positions[index - 1]?.id
      : positions.get(row.id)?.lastActivityAt !== row.lastActivityAt).map((row) => row.id))
    const anchor = initial ? this.returnAnchor : this.ports.capture()
    if (!this.region && anchor?.cursor && positions.has(anchor.id) && positions.get(anchor.id)!.positionCursor !== anchor.cursor) moved.add(anchor.id)
    const top = initial ? !anchor : !this.state.hasBefore && this.ports.atTop()
    this.ports.restore(anchor, moved, top)
    this.publish({ ...next, status: this.state.status, operation: this.state.operation, error: this.state.error, revision: this.state.revision + 1 })
    this.returnAnchor = null
  }

  /** 返回可供 React 订阅的稳定状态快照。 */
  getSnapshot = () => this.state

  /** 订阅已提交的窗口与局部请求状态。 */
  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  /** 通知视图一次完整的状态变更。 */
  private publish(change: Partial<InboxListState>) {
    this.state = { ...this.state, ...change }
    for (const listener of this.listeners) listener()
  }

  /** 失效当前队列，已发出的读取可以自然结束。 */
  dispose() {
    this.generation++
    this.queue = []
    this.deferred = null
    this.ownWritePending = false
    this.publish({ operation: null })
  }

  /** 独立详情确认失权后移除对应行，并校验批量响应的有效性。 */
  removeUnavailable(id: string) {
    if (!this.state.rowIds.includes(id) || this.state.unavailableIds.includes(id)) return
    this.generation++
    this.queue = []
    this.deferred = null
    this.ownWritePending = false
    this.ports.restore(this.ports.capture(), new Set([id]), false)
    this.publish({
      ids: this.state.ids.filter((value) => value !== id),
      positions: this.state.positions.filter((position) => position.id !== id),
      unavailableIds: [...new Set([...this.state.unavailableIds, id])],
      revision: this.state.revision + 1, operation: null,
    })
    void this.request("refresh")
  }

  /** 合并重复请求并串行执行，返回本轮队列完成或过期后的 Promise。 */
  request = (operation: InboxListOperation): Promise<void> => {
    // 变更通知触发的重读只与尚未开始的窗口重读合并，在途读取结束后必定补读一次。
    if (operation === "poll") {
      if (this.queue.includes("poll") || this.queue.includes("refresh")) return this.completion
    } else if (this.queue.includes(operation) || (this.running && this.state.operation === operation)) {
      return this.completion
    }
    this.queue.push(operation)
    if (!this.running) this.completion = this.drain()
    return this.completion
  }

  /** 本人写入成功后排入一次写入之后发起的重读，该次重读的结果立即提交顺序与版本。 */
  refreshOwnWrite = (): Promise<void> => {
    this.ownWritePending = true
    // 尚未开始的重读必定在写入之后读取，可以合并；在途重读可能早于写入，另排一次。
    if (!this.queue.includes("refresh")) this.queue.push("refresh")
    if (!this.running) this.completion = this.drain()
    return this.completion
  }

  /** 重试上次失败的操作，没有失败记录时重读原窗口。 */
  retry = () => this.request(this.state.error ?? "refresh")

  /** 补页收尾后捕获刷新范围，失败时保留已确认的窗口。 */
  private async drain() {
    if (this.running) return
    this.running = true
    const generation = this.generation
    try {
      while (this.queue.length && generation === this.generation) {
        const operation = this.queue.shift()!
        const ownWrite = operation === "refresh" && this.ownWritePending
        if (ownWrite) this.ownWritePending = false
        const window = this.deferred ?? this.state
        if ((operation === "before" && !window.hasBefore) || (operation === "after" && !window.hasAfter)) continue
        this.publish({
          operation, error: null,
          status: this.state.revision === 0 ? "initial" : operation === "before" || operation === "after" ? "loadingMore" : "refreshing",
        })
        try {
          await this.execute(operation, generation, ownWrite)
        } catch (error) {
          if (generation !== this.generation) return
          console.warn("读取收件箱窗口失败", { query: this.query, operation, error })
          this.publish({ error: operation })
        }
        if (generation === this.generation) this.publish({ status: "ready", operation: null })
      }
    } finally {
      this.running = false
      if (this.queue.length) await this.drain()
    }
  }

  /** 按同一置顶顺序版本读取整个置顶区，途中版本变化或游标失效时舍弃本轮并从首页重读。 */
  private async readRegion(): Promise<{ head: Page; window: Window }> {
    let failure: unknown = new Error("置顶顺序持续变化")
    for (let attempt = 0; attempt < 3; attempt++) {
      const head = await this.ports.page()
      const conversations = [...head.conversations]
      let page = head
      try {
        while (page.hasMore && page.pinOrderVersion === head.pinOrderVersion) {
          page = await this.ports.page(page.endCursor)
          conversations.push(...page.conversations)
        }
      } catch (error) {
        failure = error
        continue
      }
      if (page.pinOrderVersion !== head.pinOrderVersion) continue
      return { head, window: { ...head, conversations, endCursor: page.endCursor, hasBefore: false, hasAfter: false } }
    }
    throw failure
  }

  /** 串行重读连续窗口，已覆盖顶部的窗口自动扩展到最新首页。 */
  private async execute(operation: InboxListOperation, generation: number, ownWrite: boolean) {
    const base = this.deferred ?? this.state
    const initial = this.state.revision === 0
    const anchor = this.returnAnchor ?? this.ports.capture()
    const pagination = operation === "before" || operation === "after"
    let head: Page | undefined
    let appendIds: string[] = []
    let window: Window
    if (this.region) {
      ({ head, window } = await this.readRegion())
    } else if (initial && this.returnAnchor) {
      window = await this.ports.context(this.returnAnchor)
      // 定位锚点无法定位时读取首页，并从顶部展示；带原位置的锚点保留空邻域。
      if (!window.conversations.length && !this.returnAnchor.cursor) {
        this.returnAnchor = null
        head = await this.ports.page()
        window = { ...head, hasAfter: head.hasMore }
      }
    } else if (pagination) {
      const page = await this.ports.page(operation === "after" ? base.endCursor : "", operation === "before" ? base.startCursor : "")
      if (operation === "after") appendIds = page.conversations.map((row) => row.id)
      // 补页重读完整区间，以额外读取收敛上浮行和连续边界。
      window = await this.ports.window(
        operation === "before" ? page.startCursor || base.startCursor : base.startCursor,
        operation === "after" ? page.endCursor || base.endCursor : base.endCursor,
      )
    } else {
      head = await this.ports.page()
      window = !base.startCursor
        ? { ...head, hasAfter: head.hasMore }
        : await this.ports.window(base.startCursor, base.endCursor)
      // 将原先覆盖顶部的窗口扩展到最新首页，并重读完整连续范围。
      if (base.startCursor && !base.hasBefore && window.hasBefore && head.startCursor) {
        window = await this.ports.window(head.startCursor, base.endCursor)
      }
    }
    // 空区间只按本窗口内的锚点恢复。
    if (!this.region && !initial && !window.conversations.length && anchor && base.positions.some((row) => row.id === anchor.id)) window = await this.ports.context(anchor)
    if (generation !== this.generation) return
    const rowIds = [...new Set([...this.state.ids, ...window.conversations.map((row) => row.id)])].sort()
    const rows = await this.ports.rows(rowIds)
    if (generation !== this.generation) return
    this.commit(window, rows, rowIds, initial, appendIds, ownWrite)
  }

  /** 内容与资格立即更新，操作期间仅延后列表顺序和边界的布局提交。 */
  private commit(window: Window, rows: RowResults, rowIds: string[], initial: boolean, appendIds: string[], ownWrite: boolean) {
    const matching = new Set(rows.results.filter((row) => row.availability === "matching" && row.conversation).map((row) => row.id))
    const incoming = window.conversations.filter((row) => matching.has(row.id))
    const next: InboxListState = {
      ...this.state, ids: incoming.map((row) => row.id), rowIds,
      positions: incoming.map((row) => ({ id: row.id, positionCursor: row.positionCursor, lastActivityAt: row.lastActivityAt })),
      startCursor: window.startCursor, endCursor: window.endCursor,
      hasBefore: window.hasBefore, hasAfter: window.hasAfter,
      pinOrderVersion: window.pinOrderVersion,
      error: null,
    }
    const unavailable = rows.results.filter((row) => row.availability === "unavailable" && this.state.ids.includes(row.id)).map((row) => row.id)
    if (!initial && !ownWrite && this.ports.interacting()) {
      this.deferred = next
      const removed = new Set(this.state.ids.filter((id) => !matching.has(id)))
      if (removed.size) this.ports.restore(this.ports.capture(), removed, false)
      // 向下补页时立即在尾部追加新行并保持当前可见内容的位置。
      const tail = next.positions.filter((row) => appendIds.includes(row.id) && !this.state.ids.includes(row.id))
      this.publish({
        ids: [...this.state.ids.filter((id) => matching.has(id)), ...tail.map((row) => row.id)],
        positions: [...this.state.positions.filter((row) => matching.has(row.id)), ...tail],
        ...(appendIds.length ? { endCursor: next.endCursor, hasAfter: next.hasAfter } : {}),
        // 顺序版本随待应用的顺序一起提交，展示中的顺序与写入使用的版本始终是同一份快照。
        rowIds,
      })
    } else {
      this.deferred = null
      this.applyWindow(next, initial)
    }
    if (unavailable.length) this.ports.unavailable(unavailable)
  }

}

