/** 本机偏好的读写，存储不可用或内容无法解析时按未保存处理。 */

/** 读取本机保存的偏好，未保存、无法解析或存储不可用时返回 undefined。 */
export function readLocalPreference(key: string): unknown {
  try {
    const stored = localStorage.getItem(key)
    return stored === null ? undefined : JSON.parse(stored)
  } catch {
    return undefined
  }
}

/** 保存本机偏好；存储不可用时由调用方的页面状态保留当前值。 */
export function writeLocalPreference(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    return
  }
}
