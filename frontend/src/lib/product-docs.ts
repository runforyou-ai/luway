/** 产品文档页面地址：服务端在 /docs/ 下提供与当前服务器版本对应的文档，按界面语言选择中文或英文。 */

/** 应用内帮助入口引用的文档页面，值为语言目录下的页面路径。 */
export const productDocsPages = {
  home: "",
  websiteChannel: "integrations/website/",
  telegramChannel: "integrations/telegram/",
} as const

/** 应用内帮助入口可打开的文档页面。 */
export type ProductDocsPage = keyof typeof productDocsPages

/** 返回文档页面相对服务器地址的路径：中文界面使用 zh-cn，其余语言使用 en。 */
export function productDocsPath(page: ProductDocsPage, language: string) {
  const locale = language.toLowerCase().startsWith("zh") ? "zh-cn" : "en"
  return `/docs/${locale}/${productDocsPages[page]}`
}
