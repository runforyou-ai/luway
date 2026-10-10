/** 网站渠道对外入口地址和安装代码。 */

/** 返回网站渠道独立聊天链接；origin 不带末尾斜杠。 */
export function websiteChannelChatURL(origin: string, channelId: string) {
  return `${origin}/chat/${channelId}`
}

/** 返回网站渠道嵌入安装代码；origin 不带末尾斜杠。 */
export function websiteChannelWidgetSnippet(origin: string, channelId: string) {
  return `<script async src="${origin}/embed/widget.js?id=${channelId}"></script>`
}
