/** 验证 Markdown 正文中的结构化提醒强调范围。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { highlightMentions, type MarkdownNode } from "../src/lib/mention-highlight.ts"

/** 把语法树还原成带强调标记的文本，便于断言强调范围。 */
function render(node: MarkdownNode): string {
  if (node.type === "text") return node.value ?? ""
  const inner = (node.children ?? []).map(render).join("")
  return node.tagName === "span" ? `[${inner}]` : inner
}

/** 构造一棵只含指定正文的段落语法树。 */
function paragraph(value: string): MarkdownNode {
  return { type: "root", children: [{ type: "element", tagName: "p", children: [{ type: "text", value }] }] }
}

test("正文中的提醒标记被单独强调", () => {
  const tree = paragraph("已确认成本。 @后端工程师 请补充接口形态。")
  highlightMentions(["后端工程师"])()(tree)
  assert.equal(render(tree), "已确认成本。 [@后端工程师] 请补充接口形态。")
})

test("同一条正文中的多个提醒各自强调", () => {
  const tree = paragraph("@产品经理 @后端工程师 一起看下")
  highlightMentions(["产品经理", "后端工程师"])()(tree)
  assert.equal(render(tree), "[@产品经理] [@后端工程师] 一起看下")
})

test("代码块保留提醒原文", () => {
  const tree: MarkdownNode = {
    type: "root",
    children: [{
      type: "element", tagName: "pre",
      children: [{ type: "element", tagName: "code", children: [{ type: "text", value: "@后端工程师 命令原文" }] }],
    }],
  }
  highlightMentions(["后端工程师"])()(tree)
  assert.equal(render(tree), "@后端工程师 命令原文")
})

test("同名子串不构成提醒", () => {
  const tree = paragraph("邮箱 a@后端工程师b 与 @后端工程师们 都不是提醒")
  highlightMentions(["后端工程师"])()(tree)
  assert.equal(render(tree), "邮箱 a@后端工程师b 与 @后端工程师们 都不是提醒")
})

test("没有提醒目标时保持原有节点", () => {
  const tree = paragraph("普通正文")
  highlightMentions([])()(tree)
  assert.equal(render(tree), "普通正文")
})
