/** 判断消息窗口的最后一条消息是否新到达，用于清除该发送者的正在输入提示。 */

/** 已确认过的会话最后一条消息位置。 */
export type TypingArrivalBaseline = { conversationID: string; messageSeq: bigint } | null

/** 当前消息窗口的加载状态与最后一条消息序号。 */
type TypingArrivalWindow = {
  conversationID: string
  loaded: boolean
  messageSeq?: string
}

/**
 * 按窗口状态推进基线：窗口未加载时不建立基线，首次加载与切换会话只建立基线，
 * 序号超过基线才算新消息；历史定位回看更早窗口不降低基线，也不算新消息。
 */
export function nextTypingArrival(
  baseline: TypingArrivalBaseline,
  window: TypingArrivalWindow,
): { baseline: TypingArrivalBaseline; arrived: boolean } {
  if (!window.loaded) {
    return { baseline, arrived: false }
  }
  const messageSeq = window.messageSeq ? BigInt(window.messageSeq) : 0n
  if (!baseline || baseline.conversationID !== window.conversationID) {
    return { baseline: { conversationID: window.conversationID, messageSeq }, arrived: false }
  }
  if (messageSeq > baseline.messageSeq) {
    return { baseline: { conversationID: window.conversationID, messageSeq }, arrived: true }
  }
  return { baseline, arrived: false }
}
