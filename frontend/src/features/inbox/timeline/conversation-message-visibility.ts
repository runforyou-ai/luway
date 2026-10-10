/** 统一消息正文在滚动视口中的可见阈值。 */
type VerticalBounds = Pick<DOMRect, "top" | "bottom" | "height">

/** 正文至少露出一半或 32 像素时视为已看到，零尺寸不计入。 */
export function isConversationMessageVisible(row: VerticalBounds, viewport: VerticalBounds) {
  return row.height > 0 && viewport.height > 0 &&
    Math.min(viewport.bottom, row.bottom) - Math.max(viewport.top, row.top) >=
      Math.min(row.height / 2, 32)
}

/** 完整可见的消息保持原位，屏幕外消息居中，超高消息优先展示开头。 */
export function conversationMessageScrollOffset(row: VerticalBounds, viewport: VerticalBounds) {
  if (
    (row.top >= viewport.top && row.bottom <= viewport.bottom) ||
    (row.top <= viewport.top && row.bottom >= viewport.bottom)
  ) return 0
  return row.top - viewport.top - Math.max(0, (viewport.height - row.height) / 2)
}
