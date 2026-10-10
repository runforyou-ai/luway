/** 网站渠道聊天窗口首页表单校验规则。 */
import { z } from "zod"

import { WebsiteHomeBlockType } from "@/api"
import { unicodeLength } from "@/lib/text-length"

/** 判断首页链接地址是否为 http 或 https 绝对地址。 */
function isWebsiteHomeLinkURL(value: string) {
  if (value.length > 2048 || !/^https?:\/\//i.test(value)) {
    return false
  }
  try {
    return new URL(value).host !== ""
  } catch {
    return false
  }
}

/** 判断首页链接行的标题与地址是否都为空。 */
export function isBlankWebsiteHomeLink(link: { title: string; url: string }) {
  return link.title.trim() === "" && link.url.trim() === ""
}

/** 创建网站渠道聊天窗口首页校验，标题与地址都为空的链接行不校验也不提交。 */
export function createWebsiteChannelHomeSchema(messages: {
  welcomeTooLong: string
  headlineTooLong: string
  linkTitleRequired: string
  linkTitleTooLong: string
  linkURLInvalid: string
}) {
  return z.object({
    enabled: z.boolean(),
    welcome: z
      .string()
      .trim()
      .refine((value) => unicodeLength(value) <= 100, {
        message: messages.welcomeTooLong,
      }),
    headline: z
      .string()
      .trim()
      .refine((value) => unicodeLength(value) <= 100, {
        message: messages.headlineTooLong,
      }),
    blocks: z.array(
      z.object({
        type: z.enum([
          WebsiteHomeBlockType.WebsiteHomeBlockRecentConversation,
          WebsiteHomeBlockType.WebsiteHomeBlockStartConversation,
          WebsiteHomeBlockType.WebsiteHomeBlockLinks,
        ]),
        enabled: z.boolean(),
      })
    ),
    links: z
      .array(
        z
          .object({ title: z.string().trim(), url: z.string().trim() })
          .superRefine((link, ctx) => {
            if (isBlankWebsiteHomeLink(link)) return
            if (link.title === "") {
              ctx.addIssue({
                code: "custom",
                path: ["title"],
                message: messages.linkTitleRequired,
              })
            } else if (unicodeLength(link.title) > 100) {
              ctx.addIssue({
                code: "custom",
                path: ["title"],
                message: messages.linkTitleTooLong,
              })
            }
            if (!isWebsiteHomeLinkURL(link.url)) {
              ctx.addIssue({
                code: "custom",
                path: ["url"],
                message: messages.linkURLInvalid,
              })
            }
          })
      )
      .transform((links) =>
        links.filter((link) => !isBlankWebsiteHomeLink(link))
      ),
  })
}

export type WebsiteChannelHomeFormValues = z.infer<
  ReturnType<typeof createWebsiteChannelHomeSchema>
>
