/** 按当前用户语言和时区格式化日期时间。 */
import { useMemo } from "react"
import { useTranslation } from "react-i18next"

import { useUserTimeZone } from "@/contexts/user-preferences"

/** 返回按当前用户语言和时区格式化日期时间的方法。 */
export function useDateTime() {
  const { i18n } = useTranslation()
  const timeZone = useUserTimeZone()
  const formatter = useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.resolvedLanguage, {
        timeZone,
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hourCycle: "h23",
      }),
    [i18n.resolvedLanguage, timeZone],
  )

  return useMemo(() => {
    /** 按用户时区拆出年月日时分秒。 */
    function dateParts(value: string | Date) {
      return Object.fromEntries(
        formatter
          .formatToParts(new Date(value))
          .filter((part) => part.type !== "literal")
          .map((part) => [part.type, part.value]),
      )
    }

    return {
      /** 界面通用的时间格式：精确到分钟的短日期时间，如 09/21 11:12，不在今年时带上年份。 */
      formatDateTime(value: string) {
        const parts = dateParts(value)
        const date = `${parts.month}/${parts.day} ${parts.hour}:${parts.minute}`
        return parts.year === dateParts(new Date()).year
          ? date
          : `${parts.year}/${date}`
      },
      /** 带年份和秒的完整日期时间，只用于消息页的群聊资料。 */
      formatFullDateTime(value: string) {
        const parts = dateParts(value)
        return `${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}:${parts.second}`
      },
    }
  }, [formatter])
}
