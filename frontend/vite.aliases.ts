/** 应用与访客正文构建共用的模块别名。 */
import path from "path"

/** 源码目录别名，以及以空插件替代 Streamdown 引用的 rehype-raw（消息正文禁用原始 HTML）。 */
export const sharedAliases = {
  "@": path.resolve(import.meta.dirname, "./src"),
  "rehype-raw": path.resolve(import.meta.dirname, "./src/lib/rehype-raw-disabled.ts"),
}
