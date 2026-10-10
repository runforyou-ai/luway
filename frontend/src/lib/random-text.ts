/** 生成由英文字母与数字组成的随机文本。 */

/** 随机文本使用的字符。 */
const randomCharacters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

/** 生成指定长度的随机字母与数字。 */
export function randomText(length: number) {
  const values = crypto.getRandomValues(new Uint32Array(length))
  return Array.from(values, (value) => randomCharacters[value % randomCharacters.length]).join("")
}
