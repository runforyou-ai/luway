/** 按会话内消息序号维护顺序、相邻分页边界，并串行调度消息窗口的重读与补页。 */
import type { ConversationMessage, ConversationMessageList } from "@/api"

type MessageOrder = Pick<ConversationMessage, "messageSeq">

/** 无损比较同一会话中持久消息的本地序号。 */
export function compareConversationMessages(left: MessageOrder, right: MessageOrder) {
  const a = BigInt(left.messageSeq)
  const b = BigInt(right.messageSeq)
  return a < b ? -1 : a > b ? 1 : 0
}

/** 合并已验证相邻的页面，空页仅更新本次读取方向的边界。 */
export function mergeConversationPage(
  current: ConversationMessageList,
  page: ConversationMessageList,
  direction: "before" | "after",
) {
  if (current.messages.length === 0) return page
  const messages = [
    ...new Map(
      [...current.messages, ...page.messages].map((message) => [
        message.id,
        message,
      ]),
    ).values(),
  ].sort(compareConversationMessages)
  // 沿用读取结果的字段顺序，重读比较时相同内容得到相同序列化结果。
  return {
    ...page,
    messages,
    before:
      direction === "before" ? (page.before ?? current.before) : current.before,
    after:
      direction === "after" ? (page.after ?? current.after) : current.after,
    hasEarlier: direction === "before" ? page.hasEarlier : current.hasEarlier,
    hasLater: direction === "after" ? page.hasLater : current.hasLater,
  }
}

// 按对象缓存序列化结果，同一份消息只序列化一次。
const serializedMessages = new WeakMap<ConversationMessage, string>()

/** 返回消息的序列化结果。 */
function serializeMessage(message: ConversationMessage) {
  let serialized = serializedMessages.get(message)
  if (serialized === undefined) {
    serialized = JSON.stringify(message)
    serializedMessages.set(message, serialized)
  }
  return serialized
}

/** 以当前窗口为基准合入重读结果：内容未变的消息沿用原对象，整页未变时返回当前窗口。 */
function shareConversationPage(
  current: ConversationMessageList | null,
  next: ConversationMessageList,
) {
  if (!current) return next
  const previous = new Map(current.messages.map((message) => [message.id, message]))
  const messages = next.messages.map((message) => {
    const existing = previous.get(message.id)
    return existing && serializeMessage(existing) === serializeMessage(message) ? existing : message
  })
  const sameMessages =
    messages.length === current.messages.length &&
    messages.every((message, index) => message === current.messages[index])
  const sameBoundary =
    JSON.stringify({ ...next, messages: [] }) === JSON.stringify({ ...current, messages: [] })
  if (sameMessages && sameBoundary) return current
  return { ...next, messages: sameMessages ? current.messages : messages }
}

/** 消息窗口的浏览模式：跟随最新消息或停留在锚点附近。 */
type ConversationWindowMode = "latest" | "anchor"

/** 消息窗口对页面展示的当前状态。 */
type ConversationWindowSnapshot = {
  page: ConversationMessageList | null
  mode: ConversationWindowMode
  switching: boolean
  loadingDirection: "before" | "after" | null
  pageError: "before" | "after" | null
}

/** 窗口控制器依赖的读取入口、重读结果合入前保存阅读位置的回调，以及当前是否贴底跟随最新消息。 */
type ConversationWindowPorts = {
  latest: () => Promise<ConversationMessageList>
  context: (messageId: string) => Promise<ConversationMessageList>
  page: (direction: "before" | "after", cursor: string) => Promise<ConversationMessageList>
  window: (start: string, end: string) => Promise<ConversationMessageList>
  keepPosition: () => void
  followingLatest: () => boolean
}

/** 单个消息窗口的读取调度：重读与补页串行执行，打开新窗口时丢弃早于它的结果。 */
export class ConversationWindowController {
  private readonly ports: ConversationWindowPorts
  private readonly listeners = new Set<() => void>()
  private snapshot: ConversationWindowSnapshot = {
    page: null,
    mode: "latest",
    switching: false,
    loadingDirection: null,
    pageError: null,
  }
  private generation = 0
  private lane: Promise<unknown> = Promise.resolve()
  private opening: Promise<unknown> = Promise.resolve()
  private queuedRefresh: Promise<null> | null = null

  /** 绑定窗口读取入口。 */
  constructor(ports: ConversationWindowPorts) {
    this.ports = ports
  }

  /** 订阅窗口状态变化。 */
  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  /** 返回当前窗口状态。 */
  getSnapshot = () => this.snapshot

  /** 使在途定位与补页结果失效，保留当前已展示内容。 */
  cancel = () => {
    this.generation += 1
    this.update({ switching: false, loadingDirection: null })
  }

  /** 读取最新窗口或目标消息上下文；目标已在窗口内时直接完成。 */
  open = async (messageId?: string) => {
    if (messageId && this.snapshot.page?.messages.some((message) => message.id === messageId)) return true
    const generation = ++this.generation
    this.update({ switching: true, loadingDirection: null, pageError: null })
    const reading = messageId ? this.ports.context(messageId) : this.ports.latest()
    this.opening = reading.catch(() => undefined)
    try {
      const page = await reading
      if (generation !== this.generation) return false
      this.update({ page, mode: messageId && page.hasLater ? "anchor" : "latest", switching: false })
      return true
    } catch (error) {
      if (generation !== this.generation) return false
      this.update({ switching: false })
      throw error
    }
  }

  /** 按当前模式重读权威窗口；尚未开始的重复请求合并为一次。 */
  refresh = () => {
    if (!this.queuedRefresh) {
      const run = this.lane.then(() => {
        this.queuedRefresh = null
        return this.reload()
      })
      this.queuedRefresh = run
      this.lane = run.catch(() => undefined)
    }
    return this.queuedRefresh
  }

  /** 在窗口端点续读相邻页，合入前保存阅读位置。 */
  loadPage = (direction: "before" | "after", keepPosition: () => void) => {
    const base = this.snapshot.page
    if (
      !base ||
      this.snapshot.loadingDirection ||
      this.snapshot.switching ||
      !(direction === "before" ? base.hasEarlier : base.hasLater)
    )
      return Promise.resolve()
    const generation = this.generation
    this.update({ loadingDirection: direction, pageError: null })
    const run = this.lane.then(async () => {
      const current = this.snapshot.page
      const cursor = current?.[direction]
      if (
        generation !== this.generation ||
        !current ||
        !cursor ||
        !(direction === "before" ? current.hasEarlier : current.hasLater)
      ) {
        if (generation === this.generation) this.update({ loadingDirection: null })
        return
      }
      try {
        const next = await this.ports.page(direction, cursor)
        if (generation !== this.generation || this.snapshot.page !== current) return
        keepPosition()
        this.update({
          page: mergeConversationPage(current, next, direction),
          mode: direction === "after" && !next.hasLater ? "latest" : this.snapshot.mode,
        })
      } catch (error) {
        if (generation !== this.generation) return
        this.update({ pageError: direction })
        throw error
      } finally {
        if (generation === this.generation) this.update({ loadingDirection: null })
      }
    })
    this.lane = run.catch(() => undefined)
    return run
  }

  /** 首次、空窗口或贴底跟随最新消息时读取最新页，窗口随之收缩到最新一页；其余按首尾游标重读，最新模式继续补齐到尾端；窗口已被替换时丢弃结果。 */
  private async reload(): Promise<null> {
    await this.opening
    const { page: current, mode } = this.snapshot
    try {
      let next: ConversationMessageList
      for (;;) {
        const shrink = Boolean(current?.before && current.after) && mode === "latest" && this.ports.followingLatest()
        next = current?.before && current.after && !shrink
          ? await this.ports.window(current.before, current.after)
          : await this.ports.latest()
        while (mode === "latest" && next.hasLater && next.after && this.snapshot.page === current) {
          next = mergeConversationPage(next, await this.ports.page("after", next.after), "after")
        }
        // 定位读取期间暂不合入，定位成功替换窗口后本次结果作废。
        while (this.snapshot.switching) await this.opening
        if (this.snapshot.page !== current) return null
        // 读取期间离开底部时丢弃最新页，改按原首尾游标重读，保留正在阅读的历史。
        if (!shrink || this.ports.followingLatest()) break
      }
      const shared = shareConversationPage(current, next)
      if (shared === current) return null
      if (current) this.ports.keepPosition()
      this.update({ page: shared, mode: shared.hasLater ? mode : "latest" })
      return null
    } catch (error) {
      if (this.snapshot.page === current) throw error
      return null
    }
  }

  /** 替换窗口状态并通知订阅方。 */
  private update(patch: Partial<ConversationWindowSnapshot>) {
    this.snapshot = { ...this.snapshot, ...patch }
    for (const listener of this.listeners) listener()
  }
}
