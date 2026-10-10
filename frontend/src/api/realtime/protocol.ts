/** 实时事件的解码规则：先校验协议主版本，忽略未定义的事件种类，结构错误单独返回；事件契约与解码模式由 internal/realtime/protocol 生成。 */
import { z } from "zod"

import type { RunStreamBlock, RunStreamPlanTask, ConversationType } from "@/api/generated/contract"
import {
  FrameSchema,
  Type,
  Version,
  type ConversationChange,
  type ConversationChanged,
  type Frame,
  type RunStreamDelta,
  type RunStreamOperation as RunStreamOperationData,
} from "@/api/generated/realtime"

export type { NotificationView } from "@/api/generated/contract"
export type { RunStreamBlock, RunStreamPlanTask, RunStreamToolCall } from "@/api/generated/contract"

/** 实时通知中的会话类型。 */
export type RealtimeConversationType = ConversationType

/** 会话变更的变化类别。 */
export type RealtimeConversationChange = ConversationChange

/** 可按顺序应用到运行过程流快照的一条变更，按操作类型携带所需字段。 */
export type RunStreamOperation =
  | { kind: "upsert_block"; block: RunStreamBlock }
  | { kind: "append_block_text"; blockId: string; text: string }
  | { kind: "remove_blocks"; blockIds: string[] }
  | { kind: "append_candidate"; text: string }
  | { kind: "clear_candidate" }
  | { kind: "set_plan"; plan: RunStreamPlanTask[] }

/** 成员事件流交付的事件：会话变更带会话类型，运行过程流增量的操作按类型收窄。 */
export type RealtimeServerFrame =
  | Exclude<Frame, { type: "conversation_changed" | "run_stream_delta" }>
  | (ConversationChanged & { conversationType: ConversationType })
  | (Omit<RunStreamDelta, "operations"> & { operations: RunStreamOperation[] })

/** 事件解码结果：未定义的事件种类忽略，主版本不一致与结构错误分别返回。 */
type RealtimeServerFrameResult =
  | { status: "frame"; frame: RealtimeServerFrame }
  | { status: "ignored" }
  | { status: "unsupported_version" }
  | { status: "invalid"; reason: string }

/** 已定义的事件种类。 */
const frameTypes = new Set<string>(Object.values(Type))

/** 解码一条实时事件文本；先校验协议主版本，再校验事件种类、结构和字段类型。 */
export function decodeServerFrame(text: string): RealtimeServerFrameResult {
  let value: unknown
  try {
    value = JSON.parse(text)
  } catch (error) {
    return { status: "invalid", reason: error instanceof Error ? error.message : String(error) }
  }
  if (!isObject(value) || (value.v !== undefined && typeof value.v !== "number")) {
    return { status: "invalid", reason: "frame envelope is malformed" }
  }
  if (value.v !== Version) {
    return { status: "unsupported_version" }
  }
  const data = value.data ?? {}
  if (typeof value.type !== "string" || !isObject(data)) {
    return { status: "invalid", reason: "frame type or data is malformed" }
  }
  if (!frameTypes.has(value.type)) {
    return { status: "ignored" }
  }
  const parsed = FrameSchema.safeParse({ ...data, type: value.type })
  if (!parsed.success) {
    return { status: "invalid", reason: z.prettifyError(parsed.error) }
  }
  const frame = parsed.data
  switch (frame.type) {
    case "conversation_changed": {
      const conversationType = frame.conversationType
      // 成员事件流的会话变更必带会话类型。
      if (!conversationType) return { status: "invalid", reason: "conversationType is missing" }
      return { status: "frame", frame: { ...frame, conversationType } }
    }
    case "run_stream_delta": {
      const operations = frame.operations.map(readOperation)
      if (operations.some((operation) => !operation)) {
        return { status: "invalid", reason: "upsert_block operation has no block" }
      }
      return { status: "frame", frame: { ...frame, operations: operations as RunStreamOperation[] } }
    }
    default:
      return { status: "frame", frame }
  }
}

/** 按操作类型取出所需字段，插入或更新内容块的操作缺少内容块时返回 undefined。 */
function readOperation(operation: RunStreamOperationData): RunStreamOperation | undefined {
  switch (operation.kind) {
    case "upsert_block":
      return operation.block ? { kind: operation.kind, block: operation.block } : undefined
    case "append_block_text":
      return { kind: operation.kind, blockId: operation.blockId, text: operation.text }
    case "remove_blocks":
      return { kind: operation.kind, blockIds: operation.blockIds }
    case "append_candidate":
      return { kind: operation.kind, text: operation.text }
    case "clear_candidate":
      return { kind: operation.kind }
    case "set_plan":
      return { kind: operation.kind, plan: operation.plan }
  }
}

/** 判断值是否为 JSON 对象。 */
function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
