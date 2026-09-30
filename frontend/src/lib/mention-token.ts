/** 维护正文中结构化 @ 标记的匹配规则和草稿位置。 */

export type MentionAllToken = {
  start: number
  text: string
}

/** 跟随正文编辑移动所有人标记，修改或删除选中标记时取消提醒。 */
export function reconcileMentionAllToken(
  token: MentionAllToken | null,
  previousBody: string,
  nextBody: string,
  caret: number,
): MentionAllToken | null {
  if (!token || previousBody === nextBody) return token
  // 以编辑后的光标限制公共后缀，区分选中标记与同名手写文本。
  let suffix = 0
  while (
    suffix < nextBody.length - caret &&
    suffix < previousBody.length &&
    previousBody[previousBody.length - suffix - 1] ===
      nextBody[nextBody.length - suffix - 1]
  ) {
    suffix++
  }
  let prefix = 0
  while (
    prefix < previousBody.length - suffix &&
    prefix < nextBody.length - suffix &&
    previousBody[prefix] === nextBody[prefix]
  ) {
    prefix++
  }
  const previousEnd = previousBody.length - suffix
  const tokenEnd = token.start + token.text.length
  if (prefix < tokenEnd && previousEnd > token.start) return null
  const start =
    previousEnd <= token.start
      ? token.start + nextBody.length - previousBody.length
      : token.start
  const before = nextBody.slice(0, start)
  const after = nextBody.slice(start + token.text.length)
  if (
    nextBody.slice(start, start + token.text.length) !== token.text ||
    /\S$/u.test(before) ||
    /^[\p{L}\p{M}\p{N}_]/u.test(after)
  ) {
    return null
  }
  return { ...token, start }
}

/** 生成一个或多个成员姓名对应的完整 @ 标记正则片段。 */
export function mentionTokenPattern(displayNames: string[]) {
  const alternatives = displayNames
    .map((name) => name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
    .join("|")
  return `(?<!\\S)@(?:${alternatives})(?![\\p{L}\\p{M}\\p{N}_])`
}
