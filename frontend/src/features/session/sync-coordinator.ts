/** 登录会话级同步协调器：把实时通知与兜底探针结果转成资源 key 失效，并合并短窗口内的重复失效。 */
import type { SyncHeads } from "@/api"
import type { RealtimeConversationChange, RealtimeConversationType, RealtimeServerFrame } from "@/api/realtime/protocol"
// 前端单元测试由 node 直接加载，运行时依赖使用相对路径。
import { resourceKeys } from "../../hooks/resource-keys.ts"

type ResourceKey = readonly unknown[]

/** 协调器依赖的缓存失效、失败重试、探针读取与错误处理入口；matches 存在时只失效完整 key 满足条件的查询。 */
type SyncCoordinatorPorts = {
  invalidate: (key: ResourceKey, matches?: (queryKey: readonly unknown[]) => boolean) => void
  retry: () => void
  readHeads: () => Promise<SyncHeads>
  failed: (error: unknown) => void
}

/** 合并失效的窗口与兜底探针周期。 */
type SyncCoordinatorTiming = {
  invalidationWindowMs: number
  probeIntervalMs: number
}

const defaultTiming: SyncCoordinatorTiming = {
  invalidationWindowMs: 300,
  probeIntervalMs: 30_000,
}

/** 未声明变化类别的会话变更按全部类别处理。 */
const allConversationChanges: readonly RealtimeConversationChange[] = ["timeline", "service", "participants"]

/** 返回收件箱列表、列表行、提醒总数、已归档聊天、最近会话摘要与搜索结果的失效前缀。 */
function inboxKeys(): ResourceKey[] {
  return [
    resourceKeys.inbox(),
    ...inboxDerivedKeys(),
  ]
}

/** 返回列表行、提醒总数、已归档聊天、最近会话摘要与搜索结果的失效前缀。 */
function inboxDerivedKeys(): ResourceKey[] {
  return [
    resourceKeys.inboxConversations(),
    resourceKeys.archivedConversations(),
    resourceKeys.inboxAttention(),
    resourceKeys.recentConversations(),
    resourceKeys.inboxSearch(),
  ]
}

/** 返回待补知识或 AI 员工负责人变化时需要重读的待补知识清单与详情、本人负责的条数、报表与问题会话；报表与问题会话的「我负责的」范围随负责人变化。 */
function knowledgeGapKeys(): ResourceKey[] {
  return [
    resourceKeys.knowledgeGaps(),
    resourceKeys.knowledgeGap(),
    resourceKeys.aiPerformanceReport(),
    resourceKeys.aiPerformanceBreakdowns(),
    resourceKeys.aiPerformanceIssues(),
  ]
}

/** 返回指定类型会话变化时需要重读的服务收件箱与收件箱衍生 key：客户会话与可能承载服务会话的 AI 聊天重读服务会话范围，Copilot 线程只重读线程列表；聊天列表按会话类型另行登记。 */
function inboxKeysFor(conversationType: RealtimeConversationType): ResourceKey[] {
  switch (conversationType) {
    case "channel":
    case "agent":
      return [resourceKeys.inbox({ scope: "pending" }), resourceKeys.inbox({ scope: "all" }), ...inboxDerivedKeys()]
    case "direct":
    case "group":
      return inboxDerivedKeys()
    case "copilot":
      return [resourceKeys.serviceCopilotThreads()]
  }
}

/** 返回本人身份资料变化时需要重读的资源 key，包括展示本人名称和头像的成员目录。 */
function identityProfileKeys(): ResourceKey[] {
  return [
    resourceKeys.identity(),
    resourceKeys.users(),
    resourceKeys.user(),
    resourceKeys.colleagues(),
    resourceKeys.teamMembers(),
  ]
}

/** 返回兜底探针不一致时全部会话资源的失效前缀。 */
function allConversationKeys(): ResourceKey[] {
  return [
    resourceKeys.conversationSummary(),
    resourceKeys.conversationMessages(),
    resourceKeys.conversationMessagePage(),
    resourceKeys.conversationNavigation(),
    resourceKeys.conversationMentions(),
    resourceKeys.requesterProfile(),
    resourceKeys.serviceBusinessQueries(),
    resourceKeys.serviceSummaries(),
    resourceKeys.groupConversation(),
    resourceKeys.directConversation(),
    resourceKeys.serviceCopilotThreads(),
    resourceKeys.responsibleKnowledgeGapCount(),
    resourceKeys.agentServiceSessions(),
  ]
}

/** 返回单个会话按变化类别需要重读的资源 key：摘要总是重读；时间线与参与方变化重读消息窗口；服务会话的服务周期变化重读业务查询、小结与服务记录，参与方变化重读发起人资料、小结与服务记录；群聊与单聊的参与方变化重读群资料与单聊查找。 */
function conversationKeys(
  conversationId: string,
  conversationType: RealtimeConversationType,
  changes: readonly RealtimeConversationChange[],
): ResourceKey[] {
  const timeline = changes.includes("timeline")
  const service = changes.includes("service")
  const participants = changes.includes("participants")
  const keys: ResourceKey[] = [resourceKeys.conversationSummary(conversationId)]
  if (timeline || participants) {
    keys.push(
      resourceKeys.conversationMessages(conversationId),
      resourceKeys.conversationMessagePage(conversationId),
      resourceKeys.conversationNavigation(conversationId),
      resourceKeys.conversationMentions(conversationId),
    )
  }
  if (conversationType === "channel" || conversationType === "agent") {
    if (service) {
      keys.push(resourceKeys.serviceBusinessQueries(conversationId))
    }
    if (participants) {
      keys.push(resourceKeys.requesterProfile(conversationId))
    }
    if (service || participants) {
      keys.push(resourceKeys.serviceSummaries(conversationId), resourceKeys.agentServiceSessions())
    }
  }
  if (conversationType === "group" && participants) {
    keys.push(resourceKeys.groupConversation(conversationId))
  }
  if (conversationType === "direct" && participants) {
    // 单聊查找按对端身份缓存，单聊变化时重读全部单聊查找。
    keys.push(resourceKeys.directConversation())
  }
  return keys
}

/** 返回聊天列表是否展示指定类型的会话：未限定类型或类型筛选包含该类型。 */
function chatListIncludes(queryKey: readonly unknown[], kinds: ReadonlySet<RealtimeConversationType>) {
  const parameters = queryKey[1] as { kinds?: unknown } | undefined
  const listed = parameters?.kinds
  return !Array.isArray(listed) || listed.length === 0 || listed.some((kind) => kinds.has(kind))
}

/** 登录会话内唯一的同步协调器，随登录外壳创建与销毁。 */
export class SyncCoordinator {
  private readonly ports: SyncCoordinatorPorts
  private readonly timing: SyncCoordinatorTiming
  private readonly pending = new Map<string, ResourceKey>()
  private readonly pendingChatKinds = new Set<RealtimeConversationType>()
  private flushTimer: ReturnType<typeof setTimeout> | undefined
  private probeTimer: ReturnType<typeof setInterval> | undefined
  private heads: SyncHeads | null = null
  private headsRevision = 0
  private appliedRevision = 0
  private probing = false
  private probeAgain = false
  private disposed = false

  /** 创建协调器，start 之后开始周期探针。 */
  constructor(ports: SyncCoordinatorPorts, timing: Partial<SyncCoordinatorTiming> = {}) {
    this.ports = ports
    this.timing = { ...defaultTiming, ...timing }
  }

  /** 立即执行一次兜底校验，之后按固定周期执行，直到 suspend 或 dispose。 */
  start() {
    if (this.disposed || this.probeTimer !== undefined) {
      return
    }
    this.probeTimer = setInterval(() => void this.probe(), this.timing.probeIntervalMs)
    void this.probe()
  }

  /** 停止周期探针，实时通知照常处理；再次 start 时立即校验一次。 */
  suspend() {
    clearInterval(this.probeTimer)
    this.probeTimer = undefined
    this.probeAgain = false
  }

  /** 停止周期探针与待合并的失效，之后到达的通知与探针结果一律丢弃。 */
  dispose() {
    this.disposed = true
    clearInterval(this.probeTimer)
    clearTimeout(this.flushTimer)
    this.probeTimer = undefined
    this.flushTimer = undefined
    this.pending.clear()
    this.pendingChatKinds.clear()
  }

  /** 处理一条服务端事件：连接问候携带探针值，变更通知映射为对应资源 key 的失效。 */
  receive(frame: RealtimeServerFrame) {
    switch (frame.type) {
      case "server_hello":
        this.headsRevision += 1
        this.applyHeads(frame.syncHeads, this.headsRevision)
        // 待补知识与 AI 员工记忆没有同步探针，连接建立时重读断线期间可能错过的变化。
        this.enqueue([...knowledgeGapKeys(), resourceKeys.agentMemories()])
        return
      case "knowledge_gaps_changed":
        this.enqueue(knowledgeGapKeys())
        return
      case "service_reports_changed":
        this.enqueue([
          resourceKeys.aiPerformanceReport(),
          resourceKeys.aiPerformanceBreakdowns(),
          resourceKeys.aiPerformanceIssues(),
          resourceKeys.teamPerformanceReport(),
          resourceKeys.teamPerformanceMembers(),
          resourceKeys.teamPerformanceBreakdowns(),
          resourceKeys.teamPerformanceIssues(),
          resourceKeys.serviceIssue(),
        ])
        return
      case "conversation_changed":
        // 聊天列表只重读展示该类型会话的列表。
        if (frame.conversationType === "direct" || frame.conversationType === "group" || frame.conversationType === "agent") {
          this.enqueueChat(frame.conversationType)
        }
        this.enqueue([
          ...inboxKeysFor(frame.conversationType),
          ...conversationKeys(frame.conversationId, frame.conversationType, frame.changes ?? allConversationChanges),
        ])
        return
      case "conversation_state_changed":
        // 群资料携带本人免打扰状态，个人会话状态变化时一并重读。
        this.enqueue([
          ...inboxKeys(),
          resourceKeys.conversationSummary(frame.conversationId),
          resourceKeys.conversationNavigation(frame.conversationId),
          resourceKeys.conversationMentions(frame.conversationId),
          resourceKeys.groupConversation(frame.conversationId),
        ])
        return
      case "conversation_removed":
        // 独立摘要与群资料重读确认阅读资格，失权后由其消费方清理会话资源。
        this.enqueue([
          ...inboxKeys(),
          resourceKeys.conversationSummary(frame.conversationId),
          resourceKeys.groupConversation(frame.conversationId),
        ])
        return
      case "identity_profile_changed":
        this.enqueue(identityProfileKeys())
        return
      case "pin_order_changed":
        // 个人置顶顺序变化使置顶区游标失效，两个分区一并整区重读。
        this.enqueue(inboxKeys())
        return
      case "agent_memory_changed":
        this.enqueue([resourceKeys.agentMemories(frame.agentId)])
        return
    }
  }

  /** 读取一次同步探针；已有探针在途时于其结束后再读取一次。 */
  probe = async () => {
    if (this.disposed) {
      return
    }
    if (this.probing) {
      this.probeAgain = true
      return
    }
    this.probing = true
    this.headsRevision += 1
    const revision = this.headsRevision
    try {
      this.applyHeads(await this.ports.readHeads(), revision)
    } catch (error) {
      if (!this.disposed) {
        this.ports.failed(error)
      }
    } finally {
      this.probing = false
      if (this.probeAgain && !this.disposed) {
        this.probeAgain = false
        void this.probe()
      }
    }
  }

  /** 与上次探针值比较，不一致的部分失效对应资源；早于已应用结果发起的读取直接丢弃。 */
  private applyHeads(heads: SyncHeads, revision: number) {
    if (this.disposed || revision < this.appliedRevision) {
      return
    }
    this.appliedRevision = revision
    const previous = this.heads
    this.heads = heads
    // 首个探针值无法确认已读取的数据是否早于它，按不一致处理。
    if (
      !previous ||
      previous.conversationCount !== heads.conversationCount ||
      previous.conversationChecksum !== heads.conversationChecksum
    ) {
      this.enqueue([...inboxKeys(), ...allConversationKeys()])
    }
    if (!previous || previous.identityProfileVersion !== heads.identityProfileVersion) {
      this.enqueue(identityProfileKeys())
    }
    if (!previous || previous.pinOrderVersion !== heads.pinOrderVersion) {
      this.enqueue(inboxKeys())
    }
    // 探针值一致时同样开启合并窗口，窗口结束时重试上次失败的同步读取。
    this.enqueue([])
  }

  /** 登记待失效的 key，并在合并窗口结束时统一失效。 */
  private enqueue(keys: ResourceKey[]) {
    if (this.disposed) {
      return
    }
    for (const key of keys) {
      this.pending.set(JSON.stringify(key), key)
    }
    this.flushTimer ??= setTimeout(() => this.flush(), this.timing.invalidationWindowMs)
  }

  /** 登记展示指定类型会话的聊天列表，在合并窗口结束时与同批其他类型一起失效。 */
  private enqueueChat(kind: RealtimeConversationType) {
    if (this.disposed) {
      return
    }
    this.pendingChatKinds.add(kind)
    this.flushTimer ??= setTimeout(() => this.flush(), this.timing.invalidationWindowMs)
  }

  /** 失效本窗口登记的 key，已被同批较短前缀覆盖的 key 不重复失效，之后重试失败的同步读取。 */
  private flush() {
    this.flushTimer = undefined
    const keys = [...this.pending.values()].sort((left, right) => left.length - right.length)
    const chatKinds = new Set(this.pendingChatKinds)
    this.pending.clear()
    this.pendingChatKinds.clear()
    const prefixes: ResourceKey[] = []
    // covered 判断 key 是否已被同批较短前缀覆盖。
    const covered = (key: ResourceKey) => prefixes.some((prefix) =>
      prefix.every((part, index) => JSON.stringify(part) === JSON.stringify(key[index])),
    )
    for (const key of keys) {
      if (!covered(key)) {
        prefixes.push(key)
        this.ports.invalidate(key)
      }
    }
    const chatKey = resourceKeys.inbox({ scope: "chat" })
    if (chatKinds.size > 0 && !covered(chatKey)) {
      this.ports.invalidate(chatKey, (queryKey) => chatListIncludes(queryKey, chatKinds))
    }
    this.ports.retry()
  }
}
