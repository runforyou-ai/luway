/** 客户端下载页地址：服务端在产品站语言目录下提供与当前服务器同版本的客户端安装包。 */
import { productDocsLocale } from "@/lib/product-docs"

/** 返回界面语言对应的客户端下载页相对服务器地址的访问路径。 */
export function clientDownloadPath(language: string) {
  return `/${productDocsLocale(language)}/download/`
}
