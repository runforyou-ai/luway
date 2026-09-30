/** 验证进入会话与手动未读操作在慢请求下仍按用户操作顺序保存。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { enqueueConversationUnreadChange } from "../src/api/conversation-read-queue.ts"

test("慢速进入清除不能覆盖随后设置的未读标记", async () => {
  const gate = Promise.withResolvers<void>()
  const writes: boolean[] = []
  const clear = enqueueConversationUnreadChange("enter-then-mark", async () => {
    await gate.promise
    writes.push(false)
  })
  const mark = enqueueConversationUnreadChange("enter-then-mark", async () => {
    writes.push(true)
  })
  await Promise.resolve()
  assert.deepEqual(writes, [])
  gate.resolve()
  await Promise.all([clear, mark])
  assert.deepEqual(writes, [false, true])
})

test("标记尚未完成就进入会话时最终清除标记，其他会话不受阻塞", async () => {
  const gate = Promise.withResolvers<void>()
  let markedUnread = false
  const mark = enqueueConversationUnreadChange("mark-then-enter", async () => {
    await gate.promise
    markedUnread = true
  })
  const clear = enqueueConversationUnreadChange("mark-then-enter", async () => {
    markedUnread = false
  })
  assert.equal(await enqueueConversationUnreadChange("another", async () => 7), 7)
  gate.resolve()
  await Promise.all([mark, clear])
  assert.equal(markedUnread, false)
})

test("写入失败交给原调用方，后续标记仍可成功", async () => {
  const failed = enqueueConversationUnreadChange("retry", async () => {
    throw new Error("保存失败")
  })
  const next = enqueueConversationUnreadChange("retry", async () => true)
  await assert.rejects(failed, /保存失败/)
  assert.equal(await next, true)
  assert.equal(await enqueueConversationUnreadChange("retry", async () => false), false)
})
