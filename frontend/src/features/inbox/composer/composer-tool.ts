/** 消息输入行图标按钮的共用样式与弹层定位。 */

/** 28px 见方、16px 图标，悬停时图标加深为正文色且不显示底色，点击热区向四周外扩 4px。 */
export const composerToolClass =
  "relative rounded-full text-muted-foreground hover:bg-transparent hover:text-foreground dark:hover:bg-transparent after:absolute after:-inset-1 after:content-['']"

/** 返回按钮到输入区右边界的距离，弹层按此偏移使右边缘对齐主消息区右边界。 */
export function composerAlignOffset(trigger: HTMLElement | null) {
  const composer = trigger?.closest('[data-slot="conversation-composer"]')
  return trigger && composer ? trigger.getBoundingClientRect().right - composer.getBoundingClientRect().right : 0
}
