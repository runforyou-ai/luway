/** 运行过程流内核：拼接快照分片、按序应用增量，并在缺口或断开后按退避重新请求快照。 */
import {
  decodeServerFrame,
  type RealtimeServerFrame,
  type RunStreamBlock,
  type RunStreamOperation,
  type RunStreamPlanTask,
} from "./protocol.ts"
import { reconnectDelay, type RealtimeTransport } from "./realtime-client.ts"

/** 运行过程流在某个序号上的完整展示状态。 */
export type RunStreamState = {
  runId: string
  streamId: string
  attempt: number
  sequence: bigint
  blocks: RunStreamBlock[]
  candidateContent: string
  plan: RunStreamPlanTask[]
}

/** 增量应用结果：applied 得到新状态，duplicate 已包含该增量，gap 需要重新读取快照。 */
type RunStreamApplyResult =
  | { status: "applied"; state: RunStreamState }
  | { status: "duplicate" }
  | { status: "gap" }

/** 应用一条增量：终止序号不超过当前序号视为重复，起始序号不一致、流不一致或操作无法应用时需要重新读取快照。 */
export function applyRunStreamDelta(
  state: RunStreamState,
  delta: { runId: string; streamId: string; baseSequence: bigint; sequence: bigint; operations: RunStreamOperation[] },
): RunStreamApplyResult {
  if (delta.runId !== state.runId || delta.streamId !== state.streamId) return { status: "gap" }
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

/** 客户端向订阅方发布的展示状态、本次执行结束与会话错误；delivered 表示本次请求送达过展示状态。 */
export type RunStreamEvent =
  | { type: "state"; state: RunStreamState }
  | { type: "ended"; delivered: boolean }
  | { type: "session_error"; error: unknown }

/** 请求结束原因：session 交给会话恢复入口，permanent 停止请求，transient 按退避重新请求。 */
export type RunStreamErrorKind = "session" | "permanent" | "transient"

type RunStreamClientOptions = {
  transport: RealtimeTransport
  classifyError: (error: unknown) => RunStreamErrorKind
  random?: () => number
}

const backoffMaxMs = 10_000

/** 正在累积的快照分片。 */
type PendingSnapshot = {
  runId: string
  streamId: string
  attempt: number
  sequence: bigint
  partCount: number
  next: number
  blocks: RunStreamBlock[]
  candidateContent: string
  plan: RunStreamPlanTask[]
}

/** 一条运行过程流，展示状态由服务端快照与增量驱动，终态仍以持久查询为准。 */
export class RunStreamClient {
  private readonly options: RunStreamClientOptions
  private readonly listeners = new Set<(event: RunStreamEvent) => void>()
  private attempt = 0
  private failures = 0
  private close: (() => void) | undefined
  private timer: ReturnType<typeof setTimeout> | undefined
  private pending: PendingSnapshot | undefined
  private state: RunStreamState | undefined
  // delivered 记录本次请求是否送达过展示状态，运行尚未在服务端开始时结束事件不触发重读。
  private delivered = false
  private stopped = false

  constructor(options: RunStreamClientOptions) {
    this.options = options
  }

  /** 返回最近一次收到的展示状态。 */
  get current() {
    return this.state
  }

  /** 订阅客户端事件，返回取消订阅函数。 */
  subscribe(listener: (event: RunStreamEvent) => void) {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  /** 建立运行过程流；已经开始或已停止时不重复建立。 */
  start() {
    if (this.stopped || this.close || this.timer) return
    this.connect()
  }

  /** 关闭运行过程流并取消等待中的重连，之后到达的传输回调一律丢弃。 */
  stop() {
    this.stopped = true
    this.halt()
  }

  /** 使当前连接尝试失效，关闭事件流并取消重连计时。 */
  private halt() {
    this.attempt += 1
    clearTimeout(this.timer)
    this.timer = undefined
    const close = this.close
    this.close = undefined
    this.pending = undefined
    close?.()
  }

  /** 发起一次请求，只接收属于本次尝试的传输回调。 */
  private connect() {
    this.attempt += 1
    const attempt = this.attempt
    this.pending = undefined
    this.delivered = false
    this.close = this.options.transport.open({
      frame: (text) => {
        if (attempt === this.attempt) this.receive(text)
      },
      closed: (error) => {
        if (attempt === this.attempt) this.closed(error)
      },
    })
  }

  /** 解码并处理一条服务端事件；协议主版本不受支持时停止。 */
  private receive(text: string) {
    const result = decodeServerFrame(text)
    switch (result.status) {
      case "unsupported_version":
        console.warn("运行过程流协议主版本不受支持，停止请求")
        this.stop()
        return
      case "invalid":
        console.warn("忽略无法解析的运行过程事件", { reason: result.reason })
        return
      case "ignored":
        return
    }
    const frame = result.frame
    switch (frame.type) {
      case "run_stream_snapshot":
        this.failures = 0
        this.collect(frame)
        return
      case "run_stream_delta":
        this.advance(frame)
        return
      case "run_stream_ended": {
        const delivered = this.delivered
        this.halt()
        this.emit({ type: "ended", delivered })
        // 终态由持久查询确认：订阅方据此重读运行状态并在结束后停止；运行仍在执行时按退避重新请求快照。
        this.closed(new Error("run stream ended"))
        return
      }
      default:
        return
    }
  }

  /** 累积快照分片，收齐后发布展示状态；分片不连续时重新请求。 */
  private collect(frame: Extract<RealtimeServerFrame, { type: "run_stream_snapshot" }>) {
    const pending = this.pending
    if (frame.part === 0) {
      this.pending = {
        runId: frame.runId, streamId: frame.streamId, attempt: frame.attempt, sequence: frame.sequence,
        partCount: frame.partCount, next: 1, blocks: [...frame.blocks], candidateContent: frame.candidateContent, plan: frame.plan,
      }
    } else if (pending && frame.part === pending.next && frame.streamId === pending.streamId && frame.sequence === pending.sequence) {
      pending.next += 1
      pending.blocks.push(...frame.blocks)
    } else {
      this.restart(new Error("run stream snapshot parts are out of order"))
      return
    }
    const collected = this.pending!
    if (collected.next < collected.partCount) return
    this.pending = undefined
    this.delivered = true
    this.state = {
      runId: collected.runId, streamId: collected.streamId, attempt: collected.attempt,
      sequence: collected.sequence, blocks: collected.blocks, candidateContent: collected.candidateContent,
      plan: collected.plan,
    }
    this.emit({ type: "state", state: this.state })
  }

  /** 按序应用增量；快照未就绪或出现缺口时重新请求。 */
  private advance(frame: { runId: string; streamId: string; baseSequence: bigint; sequence: bigint; operations: RunStreamOperation[] }) {
    if (!this.state || this.pending) {
      this.restart(new Error("run stream delta arrived before a complete snapshot"))
      return
    }
    const result = applyRunStreamDelta(this.state, frame)
    if (result.status === "gap") {
      this.restart(new Error("run stream delta has a sequence gap"))
      return
    }
    if (result.status === "duplicate") return
    this.state = result.state
    this.emit({ type: "state", state: this.state })
  }

  /** 关闭当前请求并按退避重新请求新的快照。 */
  private restart(error: unknown) {
    this.halt()
    if (this.stopped) return
    this.closed(error)
  }

  /** 处理请求结束：会话错误交给订阅方恢复入口，其余情况按退避等待后重新请求。 */
  private closed(error: unknown) {
    this.close = undefined
    this.pending = undefined
    if (this.stopped) return
    const kind = this.options.classifyError(error)
    if (kind === "session") {
      this.stop()
      this.emit({ type: "session_error", error })
      return
    }
    if (kind === "permanent") {
      console.info("运行过程流不可读取，停止请求", { error })
      const delivered = this.delivered
      this.stop()
      this.emit({ type: "ended", delivered })
      return
    }
    this.failures += 1
    const delay = reconnectDelay(this.failures, backoffMaxMs, this.options.random ?? Math.random)
    console.info("运行过程流已断开，等待重新请求", { delay: Math.round(delay), error })
    clearTimeout(this.timer)
    this.timer = setTimeout(() => {
      this.timer = undefined
      this.connect()
    }, delay)
  }

  /** 向全部订阅方发布客户端事件。 */
  private emit(event: RunStreamEvent) {
    for (const listener of [...this.listeners]) {
      listener(event)
    }
  }
}
