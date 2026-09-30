/** 验证两端共用的会话时间规则及用户时区的日历边界。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { createConversationTimeFormatter } from "../src/features/inbox/conversation-time.ts"

test("会话时间按刚刚、分钟、小时、昨天、星期和日期显示", () => {
  const format = createConversationTimeFormatter("zh-CN", "Asia/Shanghai", { justNow: "刚刚", yesterday: "昨天" })
  const now = new Date("2026-09-09T12:00:00+08:00")
  for (const [value, expected] of [
    [null, ""],
    ["2026-09-09T11:59:30+08:00", "刚刚"],
    ["2026-09-09T11:58:00+08:00", "2分钟前"],
    ["2026-09-09T10:00:00+08:00", "2小时前"],
    ["2026-09-08T12:00:00+08:00", "昨天"],
    ["2026-09-07T12:00:00+08:00", "周一"],
    ["2026-09-04T12:00:00+08:00", "周五"],
    ["2026-09-03T12:00:00+08:00", "9/3"],
    ["2025-09-09T12:00:00+08:00", "2025/9/9"],
  ] as const) assert.equal(format(value, now), expected)
})

test("同一时刻按用户时区判定今天和昨天", () => {
  const now = new Date("2026-09-09T01:00:00Z")
  const value = "2026-09-08T20:00:00Z"
  const labels = { justNow: "刚刚", yesterday: "昨天" }
  assert.equal(createConversationTimeFormatter("zh-CN", "Asia/Shanghai", labels)(value, now), "5小时前")
  assert.equal(createConversationTimeFormatter("zh-CN", "UTC", labels)(value, now), "昨天")
})

test("跨年和夏令时切换保留昨天的日历语义", () => {
  const format = createConversationTimeFormatter("en-US", "America/New_York", { justNow: "Just now", yesterday: "Yesterday" })
  assert.equal(format("2025-12-31T12:00:00-05:00", new Date("2026-01-01T12:00:00-05:00")), "Yesterday")
  assert.equal(format("2026-03-08T00:00:00-05:00", new Date("2026-03-09T00:00:00-04:00")), "Yesterday")
})
