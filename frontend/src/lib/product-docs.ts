/** 产品文档页面登记与地址：服务端在 /docs/ 下提供与当前服务器版本对应的文档，按界面语言选择中文或英文。 */

/** 应用内帮助入口引用的文档页面，值为语言目录下的页面路径，首页为空字符串。 */
export const productDocsPages = {
  home: "",
  websiteChannel: "integrations/channels/website",
  telegramChannel: "integrations/channels/telegram",
} as const

/** 应用内帮助入口可打开的文档页面。 */
export type ProductDocsPage = keyof typeof productDocsPages

/** 返回界面语言对应的文档语言目录：中文界面使用 zh-cn，其余语言使用 en。 */
export function productDocsLocale(language: string) {
  return language.toLowerCase().startsWith("zh") ? "zh-cn" : "en"
}

/** 返回文档页面相对服务器地址的访问路径，以斜杠结尾。 */
export function productDocsSlugPath(locale: string, slug: string) {
  return slug ? `/docs/${locale}/${slug}/` : `/docs/${locale}/`
}

/** 返回登记页面按界面语言的访问路径。 */
export function productDocsPath(page: ProductDocsPage, language: string) {
  return productDocsSlugPath(productDocsLocale(language), productDocsPages[page])
}

/** 解析文档正文中的站内链接，返回语言目录、页面路径和标题锚点；不是文档页面地址时返回 null。 */
export function parseProductDocsHref(href: string) {
  const match = /^\/docs\/([a-z-]+)\/(?:(.+?)\/)?(?:#(.*))?$/.exec(href)
  if (!match) return null
  return { locale: match[1], slug: match[2] ?? "", hash: match[3] ? decodeURIComponent(match[3]) : "" }
}
