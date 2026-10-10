/** 共享实时连接的状态与事件类型。 */
import type { RealtimeServerFrame } from "./protocol.ts"

/** 事件流连接状态；stopped 表示会话错误或协议不受支持，只能由 start 重新开始。 */
export type RealtimeState = "disconnected" | "connecting" | "ready" | "backoff" | "stopped"

/** 客户端向订阅方发布的状态变化、服务端事件与会话错误。 */
export type RealtimeClientEvent =
  | { type: "resync" }
  | { type: "state"; state: RealtimeState }
  | { type: "frame"; frame: RealtimeServerFrame }
  | { type: "session_error"; error: unknown }
