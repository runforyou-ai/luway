/** 提供浏览器时区和 IANA 时区选项。 */

/** 返回浏览器当前 IANA 时区。 */
export function resolveBrowserTimeZone() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone
}

/** 返回当前运行环境支持的全部 IANA 时区。 */
export function supportedTimeZones(current: string) {
  return Array.from(
    new Set(["UTC", current, ...Intl.supportedValuesOf("timeZone")]),
  ).sort((left, right) => left.localeCompare(right))
}
