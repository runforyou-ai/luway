/** 渠道编辑页按渠道类型扩展的页签、内容与侧栏定义。 */
import type { ComponentType, ReactNode } from "react"

import type { ChannelType, MessageChannelDetail } from "@/api"

/** 渠道编辑页的全部页签。 */
export type ChannelEditTab =
  | "basic"
  | "reception"
  | "chat-interface"
  | "usage"
  | "connection"
  | "members"

/** 渠道类型扩展的页签，form 为 true 时内容常驻挂载以保留未保存的表单内容。 */
export type ChannelEditTabSpec = {
  value: Exclude<ChannelEditTab, "basic" | "reception">
  form: boolean
}

/** 渠道类型扩展内容收到的渠道详情与回调。 */
export type ChannelEditContext<T extends ChannelType> = {
  channel: MessageChannelDetail<T>
  onUpdated: () => void
  onSavingChange: (saving: boolean) => void
}

/** 一种渠道类型的编辑页定义：通用页签之后的扩展页签、各页签内容、右侧面板与详情刷新间隔。 */
export type ChannelEditDefinition<T extends ChannelType> = {
  tabs: readonly ChannelEditTabSpec[]
  render: (tab: ChannelEditTabSpec["value"], context: ChannelEditContext<T>) => ReactNode
  aside?: (context: ChannelEditContext<T>) => ReactNode
  /** 包裹页签内容与右侧面板，承载两者共享的界面状态。 */
  Scope?: ComponentType<{ channel: MessageChannelDetail<T>; children: ReactNode }>
  /** 返回已保存详情的自动刷新间隔，saving 为连接设置保存中。 */
  refetchInterval?: (channel: MessageChannelDetail<T>, saving: boolean) => number | false
}

/** 全部渠道类型的编辑页定义。 */
export type ChannelEditDefinitions = {
  [K in ChannelType]: ChannelEditDefinition<K>
}
