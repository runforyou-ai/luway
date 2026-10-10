/** 在共享账号频道上恢复运行快照并按业务序号应用增量。 */
import { AgentRunStatus, type RunStreamSnapshot } from "@/api/generated/contract"
import { getAgentRunStreamState } from "@/api/generated/operations"
import { isApiError, isNotFoundApiError } from "@/api/client"
import { sessionPath } from "@/api/session"
import { currentSessionGeneration, requestWorkspace, subscribeSessionGeneration } from "@/api/session-scope"
import { watchRunChannel } from "./member-connection"
import { decodeServerFrame, type RealtimeServerFrame, type RunStreamOperation } from "./protocol"

/** 从服务端完整快照派生的展示状态，业务序号使用 bigint 比较。 */
export type RunStreamState = Omit<RunStreamSnapshot, "sequence"> & { sequence: bigint }

type Delta = Extract<RealtimeServerFrame, { type: "run_stream_delta" }>

/** 增量应用结果：applied 得到新状态，duplicate 已包含该增量，gap 需要重新读取快照。 */
type RunStreamApplyResult =
  | { status: "applied"; state: RunStreamState }
  | { status: "duplicate" }
  | { status: "gap" }

/** 应用一条增量：终止序号不超过当前序号视为重复，起始序号不一致、流不一致或操作无法应用时需要重新读取快照。 */
export function applyRunStreamDelta(
  state: RunStreamState,
  delta: { runId: string; streamId: string; attempt: number; baseSequence: bigint; sequence: bigint; operations: RunStreamOperation[] },
): RunStreamApplyResult {
  if (delta.runId !== state.runId || delta.streamId !== state.streamId || delta.attempt !== state.attempt) return { status: "gap" }
  if (delta.sequence <= state.sequence) return { status: "duplicate" }
  if (delta.baseSequence !== state.sequence) return { status: "gap" }
  // 在副本上应用全部操作，任一操作失败时状态保持原样。
  let blocks = [...state.blocks]
  let candidateContent = state.candidateContent
  let plan = state.plan
  for (const operation of delta.operations) {
    switch (operation.kind) {
      case "upsert_block": {
        const index = blocks.findIndex((block) => block.id === operation.block.id)
        if (index >= 0) blocks[index] = operation.block
        else blocks.push(operation.block)
        break
      }
      case "append_block_text": {
        const index = blocks.findIndex((block) => block.id === operation.blockId)
        if (index < 0) return { status: "gap" }
        blocks[index] = { ...blocks[index], text: blocks[index].text + operation.text }
        break
      }
      case "remove_blocks": {
        const removed = new Set(operation.blockIds)
        if (operation.blockIds.some((id) => !blocks.some((block) => block.id === id))) return { status: "gap" }
        blocks = blocks.filter((block) => !removed.has(block.id))
        break
      }
      case "append_candidate":
        candidateContent += operation.text
        break
      case "clear_candidate":
        candidateContent = ""
        break
      case "set_plan":
        plan = operation.plan
        break
    }
  }
  return { status: "applied", state: { ...state, sequence: delta.sequence, blocks, candidateContent, plan } }
}

/** 运行展示状态、状态清除、持久运行退出与账号会话错误。 */
export type RunStreamEvent =
  | { type: "state"; state: RunStreamState }
  | { type: "reset" }
  | { type: "ended"; delivered: boolean }
  | { type: "session_error"; error: unknown }

/** 一次运行的订阅与快照恢复，传输由账号级连接管理。 */
export class RunStreamClient {
  private readonly listeners = new Set<(event: RunStreamEvent) => void>()
  private readonly generation = currentSessionGeneration()
  private close: (() => void) | undefined
  private unwatchSession: (() => void) | undefined
  private timer: ReturnType<typeof setTimeout> | undefined
  private request: AbortController | undefined
  private ready = false
  private stopped = false
  private again = false
  private pending: Delta[] = []
  private pendingBytes = 0
  private state: RunStreamState | undefined

  /** 创建一个运行的观察者。 */
  constructor(private readonly runID: string) {}

  /** 返回最近一次收到的展示状态。 */
  get current() { return this.state }

  /** 订阅展示事件。 */
  subscribe(listener: (event: RunStreamEvent) => void) {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  /** 建立精确频道订阅，就绪后才读取首次快照。 */
  start() {
    if (this.close || this.stopped) return
    const workspaceID = requestWorkspace()
    if (!workspaceID || this.generation !== currentSessionGeneration()) return
    this.unwatchSession = subscribeSessionGeneration(() => this.stop())
    this.close = watchRunChannel(workspaceID, this.runID, {
      receive: (data) => this.receive(data),
      error: (error) => { this.emit({ type: "session_error", error }); this.stop() },
      state: (state) => {
        if (this.stopped) return
        if (state.state === "subscribed") {
          this.ready = true
          void this.refresh()
        } else if (state.state === "denied") {
          // 业务请求区分单个运行失权和整个账号会话失效。
          this.ready = true
          void this.refresh()
        } else {
          this.ready = false
          clearTimeout(this.timer)
          this.again = false
          this.request?.abort()
          this.request = undefined
          this.pending = []
          this.pendingBytes = 0
        }
      },
    })
  }

  /** 取消本运行的请求、订阅和计时器，其他频道继续使用账号连接。 */
  stop() {
    this.stopped = true
    this.request?.abort()
    this.request = undefined
    clearTimeout(this.timer)
    this.unwatchSession?.()
    this.unwatchSession = undefined
    const close = this.close
    this.close = undefined
    close?.()
    this.pending = []
    this.pendingBytes = 0
  }

  /** 暂存快照窗口内的增量，缺口与服务端失效信号触发业务恢复。 */
  private receive(data: unknown) {
    if (this.stopped || this.generation !== currentSessionGeneration()) return
    const text = JSON.stringify(data)
    const decoded = decodeServerFrame(text)
    if (decoded.status !== "frame") { void this.refresh(); return }
    const frame = decoded.frame
    if (!("runId" in frame) || frame.runId !== this.runID) return
    if (frame.type !== "run_stream_delta") { void this.refresh(); return }
    if (this.state && frame.attempt < this.state.attempt) return
    if (this.request || !this.state) {
      this.pendingBytes += text.length
      if (this.pendingBytes > 1024*1024 || this.pending.length >= 1024) {
        this.pending = []
        this.pendingBytes = 0
        this.again = true
      } else this.pending.push(frame)
      if (!this.request) void this.refresh()
      return
    }
    if (!this.advance(frame)) void this.refresh()
  }

  /** 核对持久状态与执行快照，定期读取覆盖未进入事件历史的发布失败。 */
  private async refresh() {
    if (this.stopped || !this.ready || this.generation !== currentSessionGeneration()) return
    if (this.request) { this.again = true; return }
    clearTimeout(this.timer)
    const request = new AbortController()
    this.request = request
    const current = () => !this.stopped && this.request === request && !request.signal.aborted && this.generation === currentSessionGeneration()
    try {
      const result = await getAgentRunStreamState(this.runID, request.signal)
      if (!current()) return
      if (result.status !== AgentRunStatus.Running) {
        this.emit({ type: "ended", delivered: this.state !== undefined })
        this.stop()
        return
      }
      if (this.state && result.attempt !== this.state.attempt) {
        this.state = undefined
        this.emit({ type: "reset" })
      }
      if (result.snapshot) {
        const snapshot = result.snapshot
        if (snapshot.runId !== this.runID || snapshot.attempt !== result.attempt) throw new Error("run snapshot mismatch")
        this.state = { ...snapshot, sequence: BigInt(snapshot.sequence) }
        this.emit({ type: "state", state: this.state })
        for (const delta of this.pending) {
          if (delta.attempt < snapshot.attempt) continue
          if (!this.advance(delta)) { this.again = true; break }
        }
      }
      this.pending = []
      this.pendingBytes = 0
    } catch (error) {
      if (!current()) return
      if (isApiError(error) && sessionPath(error.state)) {
        this.emit({ type: "session_error", error })
        this.stop()
      } else if (isNotFoundApiError(error)) {
        this.emit({ type: "ended", delivered: this.state !== undefined })
        this.stop()
      }
    } finally {
      if (current()) {
        this.request = undefined
        const delay = this.again ? 100 : 3000
        this.again = false
        this.timer = setTimeout(() => void this.refresh(), delay)
      }
    }
  }

  /** 应用同一尝试的连续业务增量。 */
  private advance(delta: Delta) {
    if (!this.state) return false
    const result = applyRunStreamDelta(this.state, delta)
    if (result.status === "gap") return false
    if (result.status === "applied") {
      this.state = result.state
      this.emit({ type: "state", state: this.state })
    }
    return true
  }

  /** 向当前代次的观察者发送事件。 */
  private emit(event: RunStreamEvent) {
    if (this.stopped || this.generation !== currentSessionGeneration()) return
    for (const listener of this.listeners) listener(event)
  }
}
