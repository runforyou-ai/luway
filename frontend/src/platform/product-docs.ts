/** 在系统浏览器中打开当前服务器提供的产品文档。 */
import { productDocsPath, type ProductDocsPage } from "@/lib/product-docs"
import { resolveServerURL } from "@/lib/server-url"
import { openExternalURL } from "@/platform/external-navigation"

/** 按界面语言打开当前服务器上的产品文档页面。 */
export async function openProductDocs(page: ProductDocsPage, language: string) {
  await openExternalURL(`${await resolveServerURL()}${productDocsPath(page, language)}`)
}
