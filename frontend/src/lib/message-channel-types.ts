/** 消息渠道类型的界面元数据。 */
import {
  AtSignIcon,
  BookOpenIcon,
  BotIcon,
  CameraIcon,
  CodeXmlIcon,
  GlobeIcon,
  HeadsetIcon,
  MailIcon,
  MessageCircleIcon,
  MessageCircleMoreIcon,
  MessageSquareIcon,
  MessageSquareTextIcon,
  MusicIcon,
  PhoneCallIcon,
  PhoneIcon,
  SendIcon,
  ShoppingBagIcon,
  ShoppingCartIcon,
  SmartphoneIcon,
  StoreIcon,
  VideoIcon,
  type LucideIcon,
} from "lucide-react"

import { ChannelType } from "@/api"
import type { ProductDocsPage } from "@/lib/product-docs"

/** 当前支持展示的消息渠道类型；收发能力由服务端按类型给出，badgeClassName 用于实色角标，softClassName 用于浅底图标，docsPage 为接入说明的文档页面。 */
export const messageChannelTypeDefinitions = [
  {
    type: ChannelType.Website,
    translationKey: "website",
    icon: GlobeIcon,
    badgeClassName: "bg-badge-website",
    softClassName: "bg-badge-website/10 text-badge-website",
    docsPage: "websiteChannel",
  },
  {
    type: ChannelType.Telegram,
    translationKey: "telegram",
    icon: SendIcon,
    badgeClassName: "bg-badge-telegram",
    softClassName: "bg-badge-telegram/10 text-badge-telegram",
    docsPage: "telegramChannel",
  },
  {
    type: ChannelType.WechatAuthorization,
    translationKey: "wechatAuthorization",
    icon: MessageCircleIcon,
    badgeClassName: "bg-badge-wechat",
    softClassName: "bg-badge-wechat/10 text-badge-wechat",
    docsPage: "wechatAuthorizationChannel",
  },
  {
    type: ChannelType.WechatKey,
    translationKey: "wechatKey",
    icon: MessageCircleIcon,
    badgeClassName: "bg-badge-wechat",
    softClassName: "bg-badge-wechat/10 text-badge-wechat",
    docsPage: "wechatKeyChannel",
  },
  {
    type: ChannelType.WeComBot,
    translationKey: "wecomBot",
    icon: BotIcon,
    badgeClassName: "bg-badge-wecom",
    softClassName: "bg-badge-wecom/10 text-badge-wecom",
    docsPage: "wecomBotChannel",
  },
] as const satisfies readonly {
  type: ChannelType
  translationKey: string
  icon: LucideIcon
  badgeClassName: string
  softClassName: string
  docsPage: ProductDocsPage | undefined
}[]

type MessageChannelType =
  (typeof messageChannelTypeDefinitions)[number]["type"]

/** 返回消息渠道类型对应的界面元数据。 */
export function messageChannelTypeDefinition(type: ChannelType) {
  return messageChannelTypeDefinitions.find(
    (definition) => definition.type === type,
  )
}

/** 判断路由值是否为当前支持的消息渠道类型。 */
export function isMessageChannelType(
  value: string,
): value is MessageChannelType {
  return messageChannelTypeDefinitions.some(
    (definition) => definition.type === value,
  )
}

/** 尚未接入的渠道，只在添加渠道的平台选择中展示为不可选项。 */
export const plannedChannelDefinitions = [
  { key: "wechatCustomerService", icon: MessageCircleMoreIcon },
  { key: "whatsapp", icon: PhoneIcon },
  { key: "facebookMessenger", icon: MessageCircleIcon },
  { key: "instagram", icon: CameraIcon },
  { key: "email", icon: MailIcon },
  { key: "line", icon: MessageSquareIcon },
  { key: "customApi", icon: CodeXmlIcon },
  { key: "douyin", icon: MusicIcon },
  { key: "xiaohongshu", icon: BookOpenIcon },
  { key: "kuaishou", icon: VideoIcon },
  { key: "sms", icon: MessageSquareTextIcon },
  { key: "discord", icon: HeadsetIcon },
  { key: "viber", icon: PhoneCallIcon },
  { key: "kakaotalk", icon: MessageSquareIcon },
  { key: "zalo", icon: MessageSquareIcon },
  { key: "appSdk", icon: SmartphoneIcon },
  { key: "taobao", icon: ShoppingBagIcon },
  { key: "jd", icon: ShoppingCartIcon },
  { key: "pinduoduo", icon: StoreIcon },
  { key: "phone", icon: PhoneIcon },
  { key: "x", icon: AtSignIcon },
  { key: "appleMessages", icon: MessageCircleIcon },
] as const satisfies readonly { key: string; icon: LucideIcon }[]
