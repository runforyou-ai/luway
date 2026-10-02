/** 验证应用内帮助入口登记的文档页面存在，以及文档地址的生成与解析。 */
import assert from "node:assert/strict"
import { existsSync } from "node:fs"
import { test } from "node:test"
import { parseProductDocsHref, productDocsPages, productDocsPath } from "../src/lib/product-docs.ts"

const contentRoot = new URL("../../docs/", import.meta.url)

test("帮助入口引用的页面在中英文文档中都存在", () => {
  for (const locale of ["zh-cn", "en"]) {
    for (const [page, path] of Object.entries(productDocsPages)) {
      const candidates = path ? [`${locale}/${path}.md`, `${locale}/${path}/index.md`] : [`${locale}/index.md`]
      assert.ok(
        candidates.some((candidate) => existsSync(new URL(candidate, contentRoot))),
        `${page} 缺少 ${locale} 文档页面`,
      )
    }
  }
})

test("中文界面打开中文文档，其他语言打开英文文档", () => {
  assert.equal(productDocsPath("home", "zh-CN"), "/docs/zh-cn/")
  assert.equal(productDocsPath("websiteChannel", "zh"), "/docs/zh-cn/integrations/channels/website/")
  assert.equal(productDocsPath("telegramChannel", "en-US"), "/docs/en/integrations/channels/telegram/")
  assert.equal(productDocsPath("home", "ja-JP"), "/docs/en/")
})

test("解析文档正文中的站内链接", () => {
  assert.deepEqual(parseProductDocsHref("/docs/zh-cn/guide/basics/concepts/#%E5%B7%A5%E4%BD%9C%E5%8C%BA"), { locale: "zh-cn", slug: "guide/basics/concepts", hash: "工作区" })
  assert.deepEqual(parseProductDocsHref("/docs/en/"), { locale: "en", slug: "", hash: "" })
  assert.equal(parseProductDocsHref("/chat/abc"), null)
  assert.equal(parseProductDocsHref("https://example.com/docs/en/"), null)
})
