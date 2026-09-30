/** 验证正文编辑只保留从候选选择的所有人提醒。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { reconcileMentionAllToken } from "../src/lib/mention-token.ts"

test("手写与粘贴同名文本不能建立提醒", () => {
  assert.equal(reconcileMentionAllToken(null, "", "@所有人 ", 5), null)
})

test("在标记前编辑正文时移动位置，标记后编辑不改变位置", () => {
  const token = { start: 0, text: "@所有人" }
  assert.deepEqual(reconcileMentionAllToken(token, "@所有人 ", "你好 @所有人 ", 3), {
    ...token, start: 3,
  })
  assert.deepEqual(reconcileMentionAllToken(token, "@所有人 ", "@所有人 请查看", 8), token)
  assert.deepEqual(reconcileMentionAllToken({ ...token, start: 3 }, "你好 @所有人 ", "@所有人 ", 0), token)
})

test("删除或改写选中的标记后取消提醒", () => {
  const token = { start: 0, text: "@所有人" }
  assert.equal(reconcileMentionAllToken(token, "@所有人 ", "@所有 ", 3), null)
  assert.equal(reconcileMentionAllToken(token, "@所有人 ", "@某成员 ", 4), null)
  assert.equal(reconcileMentionAllToken(token, "@所有人 ", "", 0), null)
})

test("删掉选中标记后，其他同名手写文本不能接管提醒", () => {
  assert.equal(reconcileMentionAllToken(
    { start: 0, text: "@所有人" }, "@所有人 @所有人 ", "@所有人 ", 0,
  ), null)
  assert.equal(reconcileMentionAllToken(
    { start: 5, text: "@所有人" }, "@所有人 @所有人 ", "@所有人 ", 5,
  ), null)
})

test("删除同名手写文本仍保留选中的标记", () => {
  const token = { start: 0, text: "@所有人" }
  assert.deepEqual(reconcileMentionAllToken(token, "@所有人 @所有人 ", "@所有人 ", 5), token)
  assert.deepEqual(reconcileMentionAllToken(
    { ...token, start: 5 }, "@所有人 @所有人 ", "@所有人 ", 0,
  ), token)
})

test("标记与相邻文字连成普通词语时取消提醒，标点仍保留提醒", () => {
  const token = { start: 0, text: "@所有人" }
  assert.equal(reconcileMentionAllToken(token, "@所有人 ", "x@所有人 ", 1), null)
  assert.equal(reconcileMentionAllToken(token, "@所有人 ", "@所有人员 ", 5), null)
  assert.deepEqual(reconcileMentionAllToken(token, "@所有人 ", "@所有人，", 5), token)
})

test("英文标记和表情前缀保持原文并使用文本框的字符位置", () => {
  const token = { start: 0, text: "@Everyone" }
  assert.deepEqual(reconcileMentionAllToken(token, "@Everyone ", "🦌 @Everyone ", 3), {
    ...token, start: 3,
  })
  assert.deepEqual(reconcileMentionAllToken(token, "@Everyone ", "@Everyone hi", 12), token)
})
