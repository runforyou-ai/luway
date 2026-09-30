/** 验证会话输入区草稿的合并、清除与列表摘要。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { ComposerDraftStore, draftPreview } from "../src/lib/composer-draft-store.ts"

const shared = "shared" as never
const internal = "internal" as never

/** 构造一种输入模式的草稿。 */
function mode(body: string) {
  return { body, mentions: [], mentionAllToken: null }
}

test("输入模式与正文分别写入后合并为同一份会话草稿", () => {
  const store = new ComposerDraftStore()
  let changes = 0
  store.subscribe(() => changes++)
  store.update("c1", { modes: { [shared]: mode("你好") } }, shared)
  store.update("c1", { visibility: internal, replyTargets: {} }, internal)
  const draft = store.get("c1")
  assert.equal(draft?.visibility, internal)
  assert.equal(draft?.modes[shared]?.body, "你好")
  assert.equal(changes, 2)
})

test("正文与引用目标都为空时删除会话草稿，没有草稿时不通知", () => {
  const store = new ComposerDraftStore()
  let changes = 0
  store.subscribe(() => changes++)
  store.update("c1", { modes: { [shared]: mode("  ") } }, shared)
  assert.equal(changes, 0)
  store.update("c1", { modes: { [shared]: mode("草稿") } }, shared)
  store.update("c1", { modes: { [shared]: mode("") } }, shared)
  assert.equal(store.get("c1"), undefined)
  assert.equal(changes, 2)
})

test("列表摘要优先取当前输入模式的正文", () => {
  assert.equal(draftPreview(undefined), "")
  assert.equal(
    draftPreview({ visibility: internal, modes: { [shared]: mode("对客"), [internal]: mode(" 备注 ") }, replyTargets: {} }),
    "备注",
  )
  assert.equal(
    draftPreview({ visibility: internal, modes: { [shared]: mode("对客"), [internal]: mode("") }, replyTargets: {} }),
    "对客",
  )
})
