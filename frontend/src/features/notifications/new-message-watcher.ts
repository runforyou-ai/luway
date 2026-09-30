/** 按成员事件流确认的会话变化投递新消息通知：维护各会话已知末条基线，逐条投递服务端确认计入本人提醒的未读消息。 */
import type { ConversationAttentionData, ConversationAttentionMessage, InboxConversationData } from "@/api"
import type { RealtimeServerFrame } from "@/api/realtime/protocol"

/** 观察器依赖的权威读取、通知投递与错误处理入口；readAttention 在会话不可读时返回 null。 */
type NewMessageWatcherPorts = {
  readConversations: () => Promise<InboxConversationData[]>
  readAttention: (
    conversationId: string,
    afterMessageId: string,
  ) => Promise<ConversationAttentionData | null>
  deliver: (conversation: InboxConversationData, message: ConversationAttentionMessage) => Promise<void>
  failed: (error: unknown) => void
}

/** 已读确认窗口：每个会话从最近一次变化起计时，等待正在查看该会话的窗口或设备上报已读。 */
type NewMessageWatcherTiming = {
  settleWindowMs: number
}

const defaultTiming: NewMessageWatcherTiming = {
  settleWindowMs: 2000,
}

const notifiedMessageLimit = 500

/** 登录会话内唯一的新消息观察器，随登录外壳创建与销毁。 */
export class NewMessageWatcher {
  private readonly ports: NewMessageWatcherPorts
  private readonly timing: NewMessageWatcherTiming
  private readonly baselines = new Map<string, string>()
  private readonly notified = new Set<string>()
  private readonly timers = new Map<string, ReturnType<typeof setTimeout>>()
  private queue: Promise<void> = Promise.resolve()
  private connection = 0
  private seededConnection = -1
  private seedingConnection = -1
  private disposed = false

  /** 创建观察器，事件流建立后由问候事件或 start 取得首个基线。 */
  constructor(ports: NewMessageWatcherPorts, timing: Partial<NewMessageWatcherTiming> = {}) {
    this.ports = ports
    this.timing = { ...defaultTiming, ...timing }
  }

  /** 订阅时事件流已经建立的情况下立即取得首个基线。 */
  start() {
    if (this.disposed) {
      return
    }
    this.startSeed()
  }

  /** 停止处理，之后到达的事件与在途结果一律丢弃。 */
  dispose() {
    this.disposed = true
    this.clearTimers()
  }

  /** 处理一条服务端事件：连接问候开启新的连接代次并重建基线，会话变化重新开始该会话的已读确认窗口。 */
  receive(frame: RealtimeServerFrame) {
    if (this.disposed) {
      return
    }
    switch (frame.type) {
      case "server_hello":
        this.reconnected()
        return
      case "conversation_changed":
        this.changed(frame.conversationId)
        return
      case "conversation_removed":
        this.removed(frame.conversationId)
        return
    }
  }

  /** 事件流建立或重连：冷启动与重连按 catchup 处理，新代次先取当前基线，历史消息只更新未读，上一代次的在途结果与待处理会话一律丢弃。 */
  reconnected() {
    if (this.disposed) {
      return
    }
    this.connection += 1
    this.clearTimers()
    this.startSeed()
  }

  /** 会话发生可能带来新消息的变化，重新开始该会话的已读确认窗口。 */
  changed(conversationId: string) {
    if (this.disposed) {
      return
    }
    // 订阅时事件流已建立而没有问候事件时，同样先取基线再处理本次变化。
    this.startSeed()
    clearTimeout(this.timers.get(conversationId))
    this.timers.set(
      conversationId,
      setTimeout(() => this.flush(conversationId), this.timing.settleWindowMs),
    )
  }

  /** 本人失去会话的阅读资格，取消待处理的确认窗口并清除基线。 */
  removed(conversationId: string) {
    if (this.disposed) {
      return
    }
    clearTimeout(this.timers.get(conversationId))
    this.timers.delete(conversationId)
    this.baselines.delete(conversationId)
  }

  /** 当前连接代次尚未取得基线且没有在途读取时，登记一次基线读取。 */
  private startSeed() {
    if (this.seededConnection === this.connection || this.seedingConnection === this.connection) {
      return
    }
    this.seedingConnection = this.connection
    const connection = this.connection
    this.enqueue(() => this.seed(connection))
  }

  /** 读取当前会话行作为本代次基线，读取失败时按无基线处理。 */
  private async seed(connection: number) {
    try {
      const conversations = await this.ports.readConversations()
      if (this.stale(connection)) {
        return
      }
      this.baselines.clear()
      for (const conversation of conversations) {
        this.remember(conversation)
      }
    } catch (error) {
      if (this.stale(connection)) {
        return
      }
      this.baselines.clear()
      this.ports.failed(error)
    } finally {
      if (connection === this.connection) {
        this.seededConnection = connection
        this.seedingConnection = -1
      }
    }
  }

  /** 会话的已读确认窗口到期后，按到期顺序串行处理该会话；排队期间该会话再次变化时交由新的窗口处理。 */
  private flush(conversationId: string) {
    this.timers.delete(conversationId)
    const connection = this.connection
    this.enqueue(async () => {
      if (this.stale(connection) || this.timers.has(conversationId)) {
        return
      }
      try {
        await this.process(conversationId, connection)
      } catch (error) {
        this.ports.failed(error)
      }
    })
  }

  /** 取消全部未到期的已读确认窗口。 */
  private clearTimers() {
    for (const timer of this.timers.values()) {
      clearTimeout(timer)
    }
    this.timers.clear()
  }

  /** 读取变化会话中已知末条之后计入本人提醒的未读消息并逐条投递，基线尚未建立时只登记当前位置。 */
  private async process(conversationId: string, connection: number) {
    const attention = await this.ports.readAttention(conversationId, this.baselines.get(conversationId) ?? "")
    if (this.stale(connection)) {
      return
    }
    if (!attention) {
      this.baselines.delete(conversationId)
      return
    }
    if (this.seededConnection === connection) {
      for (const message of attention.messages) {
        if (this.notified.has(message.id)) {
          continue
        }
        // 投递成功后才登记，投递失败时基线保持原位，下一次事件重新投递该消息。
        await this.ports.deliver(attention.conversation, message)
        this.markNotified(message.id)
        if (this.stale(connection)) {
          return
        }
      }
    }
    // 投递全部完成后才推进基线，读取失败时保留原位置，下一次事件重新处理该范围。
    this.remember(attention.conversation)
  }

  /** 判断观察器已销毁或该结果属于过期的连接代次。 */
  private stale(connection: number) {
    return this.disposed || connection !== this.connection
  }

  /** 记录会话当前的末条消息位置。 */
  private remember(conversation: InboxConversationData) {
    if (conversation.lastMessageId) {
      this.baselines.set(conversation.id, conversation.lastMessageId)
      return
    }
    this.baselines.delete(conversation.id)
  }

  /** 登记已投递的消息编号，超出上限时淘汰最早的记录。 */
  private markNotified(messageId: string) {
    this.notified.add(messageId)
    if (this.notified.size > notifiedMessageLimit) {
      const oldest = this.notified.values().next()
      if (!oldest.done) {
        this.notified.delete(oldest.value)
      }
    }
  }

  /** 按登记顺序串行执行基线读取与会话处理，单个任务失败后队列继续接受后续任务。 */
  private enqueue(task: () => Promise<void>) {
    this.queue = this.queue
      .then(() => (this.disposed ? undefined : task()))
      .catch((error) => {
        this.ports.failed(error)
      })
  }
}
