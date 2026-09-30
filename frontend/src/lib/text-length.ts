/** 按用户可见字符计算文本长度。 */

/** 按 Unicode 字符计算长度，与服务端字符数限制一致。 */
export function unicodeLength(value: string) {
  return Array.from(value).length
}
