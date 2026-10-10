/** 在 Markdown 正文的文本节点中强调结构化提醒。 */
import { mentionTokenPattern } from "./mention-token.ts"

export type MarkdownNode = {
  type?: string
  tagName?: string
  value?: string
  properties?: Record<string, unknown>
  children?: MarkdownNode[]
}

/** 生成把提醒标记包成强调节点的 rehype 插件，代码块保持原文。 */
export function highlightMentions(names: string[]) {
  const tokens = new Set(names.map((name) => `@${name}`))
  const pattern = new RegExp(`(${mentionTokenPattern(names)})`, "gu")
  const walk = (node: MarkdownNode) => {
    if (!node.children) return
    const children: MarkdownNode[] = []
    for (const child of node.children) {
      if (child.type === "element" && (child.tagName === "code" || child.tagName === "pre")) {
        children.push(child)
        continue
      }
      if (child.type !== "text" || !child.value) {
        walk(child)
        children.push(child)
        continue
      }
      const parts = child.value.split(pattern).filter((part) => part !== "")
      if (parts.length === 1) {
        children.push(child)
        continue
      }
      for (const part of parts) {
        children.push(
          tokens.has(part)
            ? { type: "element", tagName: "span", properties: { className: ["font-semibold", "underline"] }, children: [{ type: "text", value: part }] }
            : { type: "text", value: part },
        )
      }
    }
    node.children = children
  }
  return () => (tree: MarkdownNode) => walk(tree)
}
