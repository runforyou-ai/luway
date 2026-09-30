/** 验证正文可见阈值与引用定位的滚动边界。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { conversationMessageScrollOffset, isConversationMessageVisible } from "../src/features/inbox/conversation-message-visibility.ts"

test("完整可见和达到正文阈值的消息视为已看到", () => {
  const viewport = { top: 100, bottom: 500, height: 400 }
  assert.equal(isConversationMessageVisible({ top: 200, bottom: 240, height: 40 }, viewport), true)
  assert.equal(isConversationMessageVisible({ top: 480, bottom: 520, height: 40 }, viewport), true)
  assert.equal(isConversationMessageVisible({ top: 468, bottom: 1468, height: 1000 }, viewport), true)
})

test("视口外、只露出少量正文和隐藏消息不确认", () => {
  const viewport = { top: 100, bottom: 500, height: 400 }
  for (const row of [
    { top: 20, bottom: 80, height: 60 },
    { top: 500, bottom: 540, height: 40 },
    { top: 481, bottom: 521, height: 40 },
    { top: 0, bottom: 0, height: 0 },
  ]) assert.equal(isConversationMessageVisible(row, viewport), false)
  assert.equal(isConversationMessageVisible({ top: 0, bottom: 40, height: 40 }, { top: 0, bottom: 0, height: 0 }), false)
})

test("定位完整可见消息及占满视口的长消息时保持原位", () => {
  const viewport = { top: 100, bottom: 500, height: 400 }
  for (const row of [
    { top: 200, bottom: 240, height: 40 },
    { top: 100, bottom: 140, height: 40 },
    { top: 460, bottom: 500, height: 40 },
    { top: 20, bottom: 1020, height: 1000 },
  ]) assert.equal(conversationMessageScrollOffset(row, viewport), 0)
})

test("定位屏幕外或部分可见消息时居中，长消息对齐开头", () => {
  const viewport = { top: 100, bottom: 500, height: 400 }
  assert.equal(conversationMessageScrollOffset({ top: 20, bottom: 60, height: 40 }, viewport), -260)
  assert.equal(conversationMessageScrollOffset({ top: 600, bottom: 640, height: 40 }, viewport), 320)
  assert.equal(conversationMessageScrollOffset({ top: 480, bottom: 520, height: 40 }, viewport), 200)
  assert.equal(conversationMessageScrollOffset({ top: 600, bottom: 1600, height: 1000 }, viewport), 500)
})
