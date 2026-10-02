/** 验证应用内帮助入口引用的文档页面在中英文文档中都存在。 */
import assert from "node:assert/strict"
import { existsSync } from "node:fs"
import { test } from "node:test"
import { productDocsPages, productDocsPath } from "../src/lib/product-docs.ts"

const contentRoot = new URL("../../docs/src/content/docs/", import.meta.url)

test("帮助入口引用的页面在中英文文档中都存在", () => {
  for (const locale of ["zh-cn", "en"]) {
    for (const [page, path] of Object.entries(productDocsPages)) {
      const base = path.replace(/\/$/, "")
      const candidates = base
        ? [`${locale}/${base}.md`, `${locale}/${base}.mdx`, `${locale}/${base}/index.md`, `${locale}/${base}/index.mdx`]
        : [`${locale}/index.md`, `${locale}/index.mdx`]
      assert.ok(
        candidates.some((candidate) => existsSync(new URL(candidate, contentRoot))),
        `${page} 缺少 ${locale} 文档页面`,
      )
    }
  }
})

test("中文界面打开中文文档，其他语言打开英文文档", () => {
  assert.equal(productDocsPath("home", "zh-CN"), "/docs/zh-cn/")
  assert.equal(productDocsPath("websiteChannel", "zh"), "/docs/zh-cn/integrations/website/")
  assert.equal(productDocsPath("telegramChannel", "en-US"), "/docs/en/integrations/telegram/")
  assert.equal(productDocsPath("home", "ja-JP"), "/docs/en/")
})
