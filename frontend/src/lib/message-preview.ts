/** 根据发送者身份生成列表和引用使用的可读文字。 */
import { unified } from "unified"
import remarkParse from "remark-parse"
import remarkGfm from "remark-gfm"
import { toString } from "mdast-util-to-string"
import type { OrganizationIdentityType } from "../api"

const parser = unified().use(remarkParse).use(remarkGfm)

/** 保留纯文本原意，并从 Markdown 的块级节点提取摘要。 */
export function messagePreview(body: string, senderIdentityType?: OrganizationIdentityType | null) {
  if (senderIdentityType !== "agent") return body
  const pending = [...parser.parse(body).children]
  const parts: string[] = []
  // 展开容器块，让列表条目、引用段落和表格单元格之间保留分隔。
  while (pending.length) {
    const node = pending.shift()!
    if (["list", "listItem", "blockquote", "table", "tableRow"].includes(node.type) && "children" in node) {
      pending.unshift(...node.children as typeof pending)
    } else if (node.type !== "definition" && node.type !== "footnoteDefinition") {
      parts.push(toString(node, { includeHtml: false }))
    }
  }
  return parts.filter(Boolean).join(" ").replace(/\s+/g, " ").trim()
}
