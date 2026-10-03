/** 按 Go 与 TypeScript 共用的事件夹具校验前端事件解码。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { decodeServerFrame, type RealtimeServerFrame } from "../src/api/realtime/protocol.ts"
import type {
  AgentPlanTaskStatus,
  AgentRunBlockKind,
  AgentToolCallStatus,
} from "../bindings/github.com/runforyou-ai/luway/internal/appservice/models"

type FixtureCase = {
  name: string
  result: "frame" | "ignored" | "unsupported_version" | "invalid"
  wire: unknown
}

const fixtures = JSON.parse(
  readFileSync(new URL("../../internal/realtime/protocol/testdata/frames.json", import.meta.url), "utf8"),
) as FixtureCase[]

const conversationId = "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b"
const runId = "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8b"
const streamId = "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8c"
const senderSubjectId = "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8d"

const expectedFrames: Record<string, RealtimeServerFrame> = {
  server_hello: {
    type: "server_hello",
    connectionId: "conn-01",
    syncHeads: {
      conversationCount: 3,
      conversationChecksum: "18446744073709551615",
      identityProfileVersion: "9223372036854775807",
      pinOrderVersion: "12",
    },
  },
  visitor_hello: { type: "visitor_hello", connectionId: "conn-02" },
  ping: { type: "ping" },
  conversation_changed: { type: "conversation_changed", conversationId, conversationType: "group", version: 9223372036854775807n, changes: ["timeline", "participants"] },
  conversation_state_changed: { type: "conversation_state_changed", conversationId, version: 42n },
  identity_profile_changed: { type: "identity_profile_changed", version: 9007199254740993n },
  pin_order_changed: { type: "pin_order_changed", version: 5n },
  service_attention: {
    type: "service_attention",
    conversationId,
    serviceSessionId: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a8e",
    reason: "assigned",
  },
  conversation_removed: { type: "conversation_removed", conversationId },
  conversation_typing: { type: "conversation_typing", conversationId, senderSubjectId, active: true },
  conversation_typing_stopped: { type: "conversation_typing", conversationId, senderSubjectId, active: false },
  visitor_typing: { type: "visitor_typing", conversationId, active: true },
  reception_changed: { type: "reception_changed" },
  knowledge_gaps_changed: { type: "knowledge_gaps_changed" },
  workspace_activity: {
    type: "workspace_activity",
    workspaceId: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a92",
    kind: "conversation_changed",
    conversationId,
    changes: ["timeline"],
    serviceSessionId: undefined,
    reason: undefined,
  },
  workspace_activity_service_attention: {
    type: "workspace_activity",
    workspaceId: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a92",
    kind: "service_attention",
    conversationId,
    changes: [],
    serviceSessionId: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a93",
    reason: "assigned",
  },
  service_reports_changed: { type: "service_reports_changed" },
  conversation_changed_extra_fields: { type: "conversation_changed", conversationId, conversationType: "channel", version: 7n, changes: undefined },
  ping_without_data: { type: "ping" },
  run_stream_snapshot: {
    type: "run_stream_snapshot",
    runId,
    streamId,
    attempt: 1,
    sequence: 7n,
    part: 0,
    partCount: 2,
    candidateContent: "根据知识库的记录，",
    plan: [
      { id: "1", subject: "核对退款政策", activeForm: "正在核对退款政策", status: ("in_progress" as AgentPlanTaskStatus) },
      { id: "2", subject: "整理答复", activeForm: undefined, status: ("pending" as AgentPlanTaskStatus) },
    ],
    blocks: [
      { id: "block-1", position: 1n, kind: ("thinking" as AgentRunBlockKind), text: "先确认退款政策", toolCall: undefined },
      {
        id: "block-2",
        position: 2n,
        kind: ("tool_call" as AgentRunBlockKind),
        text: "",
        toolCall: {
          name: "search_knowledge",
          status: ("running" as AgentToolCallStatus),
          startedAt: "2026-09-15T12:00:00Z",
          completedAt: undefined,
          description: undefined,
          activity: undefined,
        },
      },
      {
        id: "block-3",
        position: 3n,
        kind: ("tool_call" as AgentRunBlockKind),
        text: "",
        toolCall: {
          name: "agent",
          status: ("running" as AgentToolCallStatus),
          startedAt: "2026-09-15T12:00:00Z",
          completedAt: undefined,
          description: "查询历史订单",
          activity: "web_search",
        },
      },
    ],
  },
  run_stream_snapshot_empty: {
    type: "run_stream_snapshot",
    runId,
    streamId,
    attempt: 1,
    sequence: 0n,
    part: 0,
    partCount: 1,
    candidateContent: "",
    plan: [],
    blocks: [],
  },
  run_stream_delta: {
    type: "run_stream_delta",
    runId,
    streamId,
    attempt: 1,
    baseSequence: 7n,
    sequence: 9n,
    operations: [
      { kind: "append_block_text", blockId: "block-1", text: "，再给出答复" },
      {
        kind: "upsert_block",
        block: {
          id: "block-2",
          position: 2n,
          kind: ("tool_call" as AgentRunBlockKind),
          text: "",
          toolCall: {
            name: "search_knowledge",
            status: ("succeeded" as AgentToolCallStatus),
            startedAt: "2026-09-15T12:00:00Z",
            completedAt: "2026-09-15T12:00:03Z",
            description: undefined,
            activity: undefined,
          },
        },
      },
      { kind: "remove_blocks", blockIds: ["block-3"] },
      { kind: "clear_candidate" },
      { kind: "append_candidate", text: "退款需要在 7 天内提交。" },
      { kind: "set_plan", plan: [{ id: "1", subject: "核对退款政策", activeForm: undefined, status: ("completed" as AgentPlanTaskStatus) }] },
    ],
  },
  run_stream_ended: { type: "run_stream_ended", runId },
  computer_work: { type: "computer_work" },
  agent_memory_changed: { type: "agent_memory_changed", agentId: "0190f5a2-7c1e-7d3a-9b2f-3c4d5e6f7a91" },
}

test("事件按共用夹具解码，64 位版本不丢精度", () => {
  assert.ok(fixtures.length > 0)
  for (const fixture of fixtures) {
    const result = decodeServerFrame(JSON.stringify(fixture.wire))
    assert.equal(result.status, fixture.result, fixture.name)
    if (result.status === "frame") {
      assert.deepEqual(result.frame, expectedFrames[fixture.name], fixture.name)
    }
  }
})

test("非 JSON 文本按结构错误返回", () => {
  assert.equal(decodeServerFrame("not json").status, "invalid")
})
