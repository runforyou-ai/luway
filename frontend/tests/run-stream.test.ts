/** 校验运行过程流的快照分片拼接、增量应用与断开后的重新请求。 */
import assert from "node:assert/strict"
import { mock, test } from "node:test"
import {
  applyRunStreamDelta,
  RunStreamClient,
  type RunStreamEvent,
  type RunStreamState,
} from "../src/api/realtime/run-stream.ts"
import type { RealtimeStreamHandlers, RealtimeTransport } from "../src/api/realtime/realtime-client.ts"
import type { AgentPlanTaskStatus, AgentRunBlockKind } from "../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"

const runId = "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b"
const streamId = "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8c"
const thinking = "thinking" as AgentRunBlockKind

/** 构造一条 SSE data 文本。 */
function frame(type: string, data: Record<string, unknown>) {
  return JSON.stringify({ v: 1, type, data })
}

/** 构造一个内容块的线上结构。 */
function wireBlock(id: string, position: string, text: string) {
  return { id, position, kind: "thinking", text }
}

/** 创建记录每次请求的假传输。 */
function fakeTransport() {
  const opens: RealtimeStreamHandlers[] = []
  const closes: number[] = []
  const transport: RealtimeTransport = {
    open(handlers) {
      const index = opens.push(handlers) - 1
      return () => closes.push(index)
    },
  }
  return { opens, closes, transport }
}

/** 创建已开始的客户端与其事件记录。 */
function startClient() {
  const { opens, closes, transport } = fakeTransport()
  const events: RunStreamEvent[] = []
  const client = new RunStreamClient({ transport, classifyError: () => "transient", random: () => 0 })
  client.subscribe((event) => events.push(event))
  client.start()
  return { client, opens, closes, events }
}

/** 返回最近一次发布的展示状态。 */
function latestState(events: RunStreamEvent[]): RunStreamState {
  const state = events.filter((event) => event.type === "state").at(-1)
  assert.ok(state, "没有发布展示状态")
  return state.state
}

test("快照分片收齐后才发布展示状态", () => {
  const { opens, events } = startClient()
  opens[0].frame(frame("run_stream_snapshot", {
    runId, streamId, attempt: 1, sequence: "3", part: 0, partCount: 2,
    candidateContent: "候选", blocks: [wireBlock("block-1", "1", "甲")],
  }))
  assert.equal(events.length, 0)
  opens[0].frame(frame("run_stream_snapshot", {
    runId, streamId, attempt: 1, sequence: "3", part: 1, partCount: 2, blocks: [wireBlock("block-2", "2", "乙")],
  }))
  const state = latestState(events)
  assert.equal(state.sequence, 3n)
  assert.equal(state.candidateContent, "候选")
  assert.deepEqual(state.blocks.map((block) => block.id), ["block-1", "block-2"])
})

test("增量按序应用，重复增量不改变状态", () => {
  const { opens, events } = startClient()
  opens[0].frame(frame("run_stream_snapshot", {
    runId, streamId, attempt: 1, sequence: "3", part: 0, partCount: 1, blocks: [wireBlock("block-1", "1", "甲")],
  }))
  opens[0].frame(frame("run_stream_delta", {
    runId, streamId, attempt: 1, baseSequence: "3", sequence: "5",
    operations: [{ kind: "append_block_text", blockId: "block-1", text: "乙" }, { kind: "append_candidate", text: "正文" }],
  }))
  let state = latestState(events)
  assert.equal(state.sequence, 5n)
  assert.equal(state.blocks[0].text, "甲乙")
  assert.equal(state.candidateContent, "正文")

  const published = events.length
  opens[0].frame(frame("run_stream_delta", {
    runId, streamId, attempt: 1, baseSequence: "3", sequence: "5",
    operations: [{ kind: "append_block_text", blockId: "block-1", text: "乙" }],
  }))
  assert.equal(events.length, published)
  state = latestState(events)
  assert.equal(state.blocks[0].text, "甲乙")
})

test("增量出现缺口时关闭当前请求并重新取快照", () => {
  mock.timers.enable({ apis: ["setTimeout"] })
  try {
    const { opens, closes, events } = startClient()
    opens[0].frame(frame("run_stream_snapshot", {
      runId, streamId, attempt: 1, sequence: "3", part: 0, partCount: 1, blocks: [wireBlock("block-1", "1", "甲")],
    }))
    opens[0].frame(frame("run_stream_delta", {
      runId, streamId, attempt: 1, baseSequence: "7", sequence: "8",
      operations: [{ kind: "append_block_text", blockId: "block-1", text: "乙" }],
    }))
    assert.deepEqual(closes, [0])
    mock.timers.tick(1_000)
    assert.equal(opens.length, 2)
    // 缺口后到达的旧流事件不再影响状态。
    opens[0].frame(frame("run_stream_delta", {
      runId, streamId, attempt: 1, baseSequence: "3", sequence: "4",
      operations: [{ kind: "append_block_text", blockId: "block-1", text: "丙" }],
    }))
    assert.equal(latestState(events).blocks[0].text, "甲")
  } finally {
    mock.timers.reset()
  }
})

test("流结束后发布结束事件，运行仍在执行时按退避重新请求", () => {
  mock.timers.enable({ apis: ["setTimeout"] })
  try {
    const { opens, events } = startClient()
    opens[0].frame(frame("run_stream_ended", { runId }))
    assert.deepEqual(events, [{ type: "ended", delivered: false }])
    mock.timers.tick(1_000)
    assert.equal(opens.length, 2)
  } finally {
    mock.timers.reset()
  }
})

test("订阅方停止后不再重新请求", () => {
  mock.timers.enable({ apis: ["setTimeout"] })
  try {
    const { client, opens } = startClient()
    opens[0].closed(new Error("network"))
    client.stop()
    mock.timers.tick(30_000)
    assert.equal(opens.length, 1)
  } finally {
    mock.timers.reset()
  }
})

test("送达过展示状态后结束，结束事件标记已送达", () => {
  mock.timers.enable({ apis: ["setTimeout"] })
  try {
    const { opens, events } = startClient()
    opens[0].frame(frame("run_stream_snapshot", {
      runId, streamId, attempt: 1, sequence: "3", part: 0, partCount: 1, blocks: [wireBlock("block-1", "1", "甲")],
    }))
    opens[0].frame(frame("run_stream_ended", { runId }))
    assert.deepEqual(events.at(-1), { type: "ended", delivered: true })
    // 重新请求后在收到新快照前，结束事件不再标记已送达。
    mock.timers.tick(1_000)
    opens[1].frame(frame("run_stream_ended", { runId }))
    assert.deepEqual(events.at(-1), { type: "ended", delivered: false })
  } finally {
    mock.timers.reset()
  }
})

test("运行不可读取时停止请求并发布结束事件", () => {
  mock.timers.enable({ apis: ["setTimeout"] })
  try {
    const { opens, transport } = fakeTransport()
    const events: RunStreamEvent[] = []
    const client = new RunStreamClient({ transport, classifyError: () => "permanent", random: () => 0 })
    client.subscribe((event) => events.push(event))
    client.start()
    opens[0].closed(new Error("not found"))
    assert.deepEqual(events, [{ type: "ended", delivered: false }])
    mock.timers.tick(30_000)
    assert.equal(opens.length, 1)
  } finally {
    mock.timers.reset()
  }
})

test("移除未知内容块的增量按缺口处理", () => {
  const state: RunStreamState = {
    runId, streamId, attempt: 1, sequence: 1n, candidateContent: "", plan: [],
    blocks: [{ id: "block-1", position: 1n, kind: thinking, text: "甲" }],
  }
  const result = applyRunStreamDelta(state, {
    runId, streamId, baseSequence: 1n, sequence: 2n, operations: [{ kind: "remove_blocks", blockIds: ["block-9"] }],
  })
  assert.equal(result.status, "gap")
})

test("移除内容块只删除指定块", () => {
  const state: RunStreamState = {
    runId, streamId, attempt: 1, sequence: 1n, candidateContent: "正文", plan: [],
    blocks: [
      { id: "block-1", position: 1n, kind: thinking, text: "甲" },
      { id: "block-2", position: 2n, kind: thinking, text: "乙" },
    ],
  }
  const result = applyRunStreamDelta(state, {
    runId, streamId, baseSequence: 1n, sequence: 2n, operations: [{ kind: "remove_blocks", blockIds: ["block-2"] }],
  })
  assert.equal(result.status, "applied")
  assert.deepEqual(result.status === "applied" ? result.state.blocks.map((block) => block.id) : undefined, ["block-1"])
  assert.equal(result.status === "applied" ? result.state.candidateContent : undefined, "正文")
})

test("任务清单写入整体替换清单，内容块与正文不变", () => {
  const state: RunStreamState = {
    runId, streamId, attempt: 1, sequence: 1n, candidateContent: "正文", plan: [],
    blocks: [{ id: "block-1", position: 1n, kind: thinking, text: "甲" }],
  }
  const plan = [{ id: "1", subject: "整理报价", status: "in_progress" as AgentPlanTaskStatus }]
  const result = applyRunStreamDelta(state, {
    runId, streamId, baseSequence: 1n, sequence: 2n, operations: [{ kind: "set_plan", plan }],
  })
  assert.equal(result.status, "applied")
  assert.deepEqual(result.status === "applied" ? result.state.plan : undefined, plan)
  assert.equal(result.status === "applied" ? result.state.blocks.length : undefined, 1)
  assert.equal(result.status === "applied" ? result.state.candidateContent : undefined, "正文")
})
