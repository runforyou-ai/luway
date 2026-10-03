/** 实时 SSE 事件流的 JSON 事件契约与解码，与 internal/realtime/protocol 保持一致。 */
import type {
  AgentPlanTaskStatus,
  AgentRunBlockKind,
  AgentToolCallStatus,
  SyncHeads,
} from "../../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"

/** 运行过程流内容块的已知类型，取值与 AgentRunBlockKind 一致。 */
const blockKinds = new Set(["thinking", "content", "tool_call"])

/** 运行过程流工具调用的已知状态，取值与 AgentToolCallStatus 一致。 */
const toolCallStatuses = new Set(["queued", "running", "waiting", "succeeded", "failed", "cancelled", "interrupted", "needs_review"])

/** 运行任务清单中任务的已知状态，取值与 AgentPlanTaskStatus 一致。 */
const planTaskStatuses = new Set(["pending", "in_progress", "completed"])

/** 会话的已知类型，取值与 ConversationType 一致。 */
const conversationTypes = new Set(["channel", "direct", "agent", "group", "copilot"])

/** 实时通知中的会话类型，与 internal/domain 的 ConversationType 一致。 */
export type RealtimeConversationType = "channel" | "direct" | "agent" | "group" | "copilot"

/** 会话变更的变化类别，与 internal/domain 的 ConversationChanges 名称一致。 */
export type RealtimeConversationChange = "timeline" | "service" | "participants"

/** 会话变化类别的已知取值。 */
const conversationChanges = new Set<string>(["timeline", "service", "participants"])

/** 客服处理周期提醒的已知原因。 */
const serviceAttentionReasons = new Set([
  "assigned",
  "response_overdue",
  "queue_waiting",
  "returned",
])

/** 客服处理周期提醒原因，与 internal/domain 的 ServiceAttentionReason 一致。 */
export type ServiceAttentionReason =
  | "assigned"
  | "response_overdue"
  | "queue_waiting"
  | "returned"

/** 当前协议主版本，只有破坏性演进才提升。 */
const realtimeProtocolVersion = 1

const int64Max = 9223372036854775807n

/** 运行过程流中的工具调用名称与状态，完整参数与结果经过程详情查询读取；description 是委派调用的子任务说明，activity 是子 Agent 正在调用的工具名称。 */
export type RunStreamToolCall = {
  name: string
  status: AgentToolCallStatus
  startedAt?: string
  completedAt?: string
  description?: string
  activity?: string
}

/** 运行过程流任务清单中的一项任务。 */
export type RunStreamPlanTask = {
  id: string
  subject: string
  activeForm?: string
  status: AgentPlanTaskStatus
}

/** 运行过程流中按位置排列的展示内容块。 */
export type RunStreamBlock = {
  id: string
  position: bigint
  kind: AgentRunBlockKind
  text: string
  toolCall?: RunStreamToolCall
}

/** 可按顺序应用到运行过程流快照的一条变更。 */
export type RunStreamOperation =
  | { kind: "upsert_block"; block: RunStreamBlock }
  | { kind: "append_block_text"; blockId: string; text: string }
  | { kind: "remove_blocks"; blockIds: string[] }
  | { kind: "append_candidate"; text: string }
  | { kind: "clear_candidate" }
  | { kind: "set_plan"; plan: RunStreamPlanTask[] }

/** 服务端经事件流下发的事件。 */
export type RealtimeServerFrame =
  | { type: "server_hello"; connectionId: string; syncHeads: SyncHeads }
  | { type: "visitor_hello"; connectionId: string }
  | { type: "ping" }
  | { type: "conversation_changed"; conversationId: string; conversationType: RealtimeConversationType; version: bigint; changes?: RealtimeConversationChange[] }
  | { type: "conversation_removed"; conversationId: string }
  | { type: "conversation_state_changed"; conversationId: string; version: bigint }
  | { type: "conversation_typing"; conversationId: string; senderSubjectId: string; active: boolean }
  | { type: "visitor_typing"; conversationId: string; active: boolean }
  | { type: "reception_changed" }
  | { type: "knowledge_gaps_changed" }
  | { type: "service_reports_changed" }
  | { type: "identity_profile_changed"; version: bigint }
  | { type: "pin_order_changed"; version: bigint }
  | {
      type: "service_attention"
      conversationId: string
      serviceSessionId: string
      reason: ServiceAttentionReason
    }
  | {
      type: "run_stream_snapshot"
      runId: string
      streamId: string
      attempt: number
      sequence: bigint
      part: number
      partCount: number
      candidateContent: string
      plan: RunStreamPlanTask[]
      blocks: RunStreamBlock[]
    }
  | {
      type: "run_stream_delta"
      runId: string
      streamId: string
      attempt: number
      baseSequence: bigint
      sequence: bigint
      operations: RunStreamOperation[]
    }
  | { type: "run_stream_ended"; runId: string }
  | { type: "computer_work" }
  | { type: "agent_memory_changed"; agentId: string }
  | {
      type: "workspace_activity"
      workspaceId: string
      kind: WorkspaceActivityKind
      conversationId?: string
      changes?: RealtimeConversationChange[]
      serviceSessionId?: string
      reason?: ServiceAttentionReason
    }

/** 工作区动态携带的原成员事件种类。 */
export type WorkspaceActivityKind =
  | "conversation_changed"
  | "conversation_removed"
  | "conversation_state_changed"
  | "service_attention"
  | "identity_profile_changed"

const workspaceActivityKinds = new Set<string>([
  "conversation_changed",
  "conversation_removed",
  "conversation_state_changed",
  "service_attention",
  "identity_profile_changed",
])

/** 事件解码结果：未定义的事件种类忽略，主版本不一致与结构错误分别返回。 */
type RealtimeServerFrameResult =
  | { status: "frame"; frame: RealtimeServerFrame }
  | { status: "ignored" }
  | { status: "unsupported_version" }
  | { status: "invalid"; reason: string }

type FrameData = Record<string, unknown>

/** 解码一条 SSE data 文本；先校验协议主版本，再校验事件种类、结构和字段类型。 */
export function decodeServerFrame(text: string): RealtimeServerFrameResult {
  try {
    const value: unknown = JSON.parse(text)
    if (!isFrameData(value) || (value.v !== undefined && typeof value.v !== "number")) {
      return { status: "invalid", reason: "frame envelope is malformed" }
    }
    if (value.v !== realtimeProtocolVersion) {
      return { status: "unsupported_version" }
    }
    const data = value.data ?? {}
    if (typeof value.type !== "string" || !isFrameData(data)) {
      return { status: "invalid", reason: "frame type or data is malformed" }
    }
    const frame = decodeServerData(value.type, data)
    return frame ? { status: "frame", frame } : { status: "ignored" }
  } catch (error) {
    return { status: "invalid", reason: error instanceof Error ? error.message : String(error) }
  }
}

/** 按事件种类读取事件数据，未定义的种类返回 undefined。 */
function decodeServerData(type: string, data: FrameData): RealtimeServerFrame | undefined {
  switch (type) {
    case "ping":
    case "reception_changed":
    case "knowledge_gaps_changed":
    case "service_reports_changed":
      return { type }
    case "server_hello": {
      // 探针校验和与身份资料版本是不透明比较值，保持字符串。
      const syncHeads = data.syncHeads
      if (!isFrameData(syncHeads) || !Number.isInteger(syncHeads.conversationCount)) {
        throw new Error("syncHeads is malformed")
      }
      return {
        type,
        connectionId: readString(data, "connectionId"),
        syncHeads: {
          conversationCount: syncHeads.conversationCount as number,
          conversationChecksum: readString(syncHeads, "conversationChecksum"),
          identityProfileVersion: readString(syncHeads, "identityProfileVersion"),
          pinOrderVersion: readString(syncHeads, "pinOrderVersion"),
        },
      }
    }
    case "visitor_hello":
      return { type, connectionId: readString(data, "connectionId") }
    case "conversation_changed":
      return {
        type,
        conversationId: readString(data, "conversationId"),
        conversationType: readEnum(data, "conversationType", conversationTypes) as RealtimeConversationType,
        version: readInt64(data, "version"),
        changes: readConversationChanges(data),
      }
    case "conversation_state_changed":
      return { type, conversationId: readString(data, "conversationId"), version: readInt64(data, "version") }
    case "conversation_removed":
      return { type, conversationId: readString(data, "conversationId") }
    case "conversation_typing":
      return {
        type,
        conversationId: readString(data, "conversationId"),
        senderSubjectId: readString(data, "senderSubjectId"),
        active: readBoolean(data, "active"),
      }
    case "visitor_typing":
      return { type, conversationId: readString(data, "conversationId"), active: readBoolean(data, "active") }
    case "identity_profile_changed":
    case "pin_order_changed":
      return { type, version: readInt64(data, "version") }
    case "service_attention":
      return {
        type,
        conversationId: readString(data, "conversationId"),
        serviceSessionId: readString(data, "serviceSessionId"),
        reason: readEnum(data, "reason", serviceAttentionReasons) as ServiceAttentionReason,
      }
    case "run_stream_snapshot":
      return {
        type,
        runId: readString(data, "runId"),
        streamId: readString(data, "streamId"),
        attempt: readInt(data, "attempt"),
        sequence: readInt64(data, "sequence"),
        part: readInt(data, "part"),
        partCount: readInt(data, "partCount"),
        candidateContent: typeof data.candidateContent === "string" ? data.candidateContent : "",
        plan: readArray(data, "plan").map(readPlanTask),
        blocks: readArray(data, "blocks").map(readBlock),
      }
    case "run_stream_delta":
      return {
        type,
        runId: readString(data, "runId"),
        streamId: readString(data, "streamId"),
        attempt: readInt(data, "attempt"),
        baseSequence: readInt64(data, "baseSequence"),
        sequence: readInt64(data, "sequence"),
        operations: readArray(data, "operations").map(readOperation),
      }
    case "run_stream_ended":
      return { type, runId: readString(data, "runId") }
    case "computer_work":
      return { type }
    case "agent_memory_changed":
      return { type, agentId: readString(data, "agentId") }
    case "workspace_activity":
      return {
        type,
        workspaceId: readString(data, "workspaceId"),
        kind: readEnum(data, "kind", workspaceActivityKinds) as WorkspaceActivityKind,
        conversationId: readOptionalString(data, "conversationId"),
        changes: readConversationChanges(data),
        serviceSessionId: readOptionalString(data, "serviceSessionId"),
        reason: data.reason === undefined ? undefined : (readEnum(data, "reason", serviceAttentionReasons) as ServiceAttentionReason),
      }
    default:
      return undefined
  }
}

/** 读取一个运行过程流内容块，枚举值不在契约内时报错。 */
function readBlock(value: unknown): RunStreamBlock {
  if (!isFrameData(value)) {
    throw new Error("block is not an object")
  }
  const call = value.toolCall
  return {
    id: readString(value, "id"),
    position: readInt64(value, "position"),
    kind: readEnum(value, "kind", blockKinds) as AgentRunBlockKind,
    text: typeof value.text === "string" ? value.text : "",
    toolCall: isFrameData(call)
      ? {
          name: readString(call, "name"),
          status: readEnum(call, "status", toolCallStatuses) as AgentToolCallStatus,
          startedAt: typeof call.startedAt === "string" ? call.startedAt : undefined,
          completedAt: typeof call.completedAt === "string" ? call.completedAt : undefined,
          description: typeof call.description === "string" ? call.description : undefined,
          activity: typeof call.activity === "string" ? call.activity : undefined,
        }
      : undefined,
  }
}

/** 读取任务清单中的一项任务，状态不在契约内时报错。 */
function readPlanTask(value: unknown): RunStreamPlanTask {
  if (!isFrameData(value)) {
    throw new Error("plan task is not an object")
  }
  return {
    id: readString(value, "id"),
    subject: readString(value, "subject"),
    activeForm: typeof value.activeForm === "string" ? value.activeForm : undefined,
    status: readEnum(value, "status", planTaskStatuses) as AgentPlanTaskStatus,
  }
}

/** 读取一条增量操作，未定义的操作类型报错。 */
function readOperation(value: unknown): RunStreamOperation {
  if (!isFrameData(value)) {
    throw new Error("operation is not an object")
  }
  const kind = readString(value, "kind")
  switch (kind) {
    case "upsert_block":
      return { kind, block: readBlock(value.block) }
    case "append_block_text":
      return { kind, blockId: readString(value, "blockId"), text: readString(value, "text") }
    case "remove_blocks":
      return { kind, blockIds: readArray(value, "blockIds").map((id) => {
        if (typeof id !== "string") throw new Error("blockIds contains a non-string")
        return id
      }) }
    case "append_candidate":
      return { kind, text: readString(value, "text") }
    case "clear_candidate":
      return { kind }
    case "set_plan":
      return { kind, plan: readArray(value, "plan").map(readPlanTask) }
    default:
      throw new Error(`unsupported run stream operation ${kind}`)
  }
}

/** 判断值是否为 JSON 对象。 */
function isFrameData(value: unknown): value is FrameData {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

/** 读取字符串字段。 */
function readString(data: FrameData, key: string): string {
  const value = data[key]
  if (typeof value !== "string") {
    throw new Error(`${key} is not a string`)
  }
  return value
}

/** 读取可省略的字符串字段，缺少时返回 undefined。 */
function readOptionalString(data: FrameData, key: string): string | undefined {
  return data[key] === undefined ? undefined : readString(data, key)
}

/** 读取布尔字段。 */
function readBoolean(data: FrameData, key: string): boolean {
  const value = data[key]
  if (typeof value !== "boolean") {
    throw new Error(`${key} is not a boolean`)
  }
  return value
}

/** 读取数组字段，缺少字段时返回空数组。 */
function readArray(data: FrameData, key: string): unknown[] {
  const value = data[key]
  if (value === undefined || value === null) {
    return []
  }
  if (!Array.isArray(value)) {
    throw new Error(`${key} is not an array`)
  }
  return value
}

/** 读取非负整数字段。 */
function readInt(data: FrameData, key: string): number {
  const value = data[key]
  if (typeof value !== "number" || !Number.isInteger(value) || value < 0) {
    throw new Error(`${key} is not a non-negative integer`)
  }
  return value
}

/** 读取取值在已知集合内的字符串字段。 */
function readEnum(data: FrameData, key: string, values: Set<string>): string {
  const value = readString(data, key)
  if (!values.has(value)) {
    throw new Error(`${key} is not a known ${key} value`)
  }
  return value
}

/** 读取会话变化类别；缺少字段或含未知类别时返回 undefined，由接收方按全部类别处理。 */
function readConversationChanges(data: FrameData): RealtimeConversationChange[] | undefined {
  const value = data.changes
  if (!Array.isArray(value) || !value.every((change) => typeof change === "string" && conversationChanges.has(change))) {
    return undefined
  }
  return value as RealtimeConversationChange[]
}

/** 读取以十进制字符串传输的非负 64 位整数。 */
function readInt64(data: FrameData, key: string): bigint {
  const value = data[key]
  if (typeof value !== "string" || !/^(0|[1-9]\d*)$/.test(value) || BigInt(value) > int64Max) {
    throw new Error(`${key} is not an int64 string`)
  }
  return BigInt(value)
}
