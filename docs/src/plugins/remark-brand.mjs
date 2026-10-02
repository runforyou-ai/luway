// 把正文中的 {{product}} 替换为页面语言对应的构建品牌名称。
import { visit } from "unist-util-visit"

/** 产品名称占位符。 */
const placeholder = /\{\{\s*product\s*\}\}/g

/** 按文档语言目录替换文本、链接和代码中的产品名称占位符；names 以语言目录为键，页面不在任一语言目录下时构建失败。 */
export function remarkBrand({ names }) {
  return (tree, file) => {
    const filePath = (file.path ?? file.history[0] ?? "").replaceAll("\\", "/")
    const locale = Object.keys(names).find((key) => filePath.includes(`/content/docs/${key}/`))
    if (!locale) throw new Error(`remark-brand: ${filePath} is not under a locale directory`)
    const name = names[locale]
    visit(tree, (node) => {
      if (typeof node.value === "string") node.value = node.value.replace(placeholder, name)
      if (typeof node.url === "string") node.url = node.url.replace(placeholder, name)
      if (typeof node.title === "string") node.title = node.title.replace(placeholder, name)
    })
  }
}
