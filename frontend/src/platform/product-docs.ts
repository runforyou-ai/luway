/** 在系统浏览器中打开当前服务器提供的产品文档。 */
import { serverURL } from "@/api/client"
import { productDocsPath, type ProductDocsPage } from "@/lib/product-docs"
import { openExternalURL } from "@/platform/system"

/** 按界面语言打开当前服务器上的产品文档页面。 */
export async function openProductDocs(page: ProductDocsPage, language: string) {
  await openProductDocsPath(productDocsPath(page, language))
}

/** 打开当前服务器上相对服务器地址的文档路径。 */
export async function openProductDocsPath(path: string) {
  await openExternalURL(`${serverURL()}${path}`)
}
