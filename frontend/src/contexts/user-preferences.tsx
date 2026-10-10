/** 同步当前用户语言并提供日期时间显示时区。 */
import { createContext, useContext, useEffect, type ReactNode } from "react"

import type { CurrentUser } from "@/api"
import { changeAppLanguage } from "@/i18n"

const UserTimeZoneContext = createContext<CurrentUser["timeZone"] | null>(null)

/** 同步用户语言并向子页面提供用户时区。 */
export function UserPreferencesProvider({
  user,
  children,
}: {
  user: CurrentUser
  children: ReactNode
}) {
  useEffect(() => {
    void changeAppLanguage(user.locale).catch((error) => {
      console.warn("切换界面语言失败", error)
    })
  }, [user.locale])

  return (
    <UserTimeZoneContext.Provider value={user.timeZone}>
      {children}
    </UserTimeZoneContext.Provider>
  )
}

/** 返回当前用户的日期时间显示时区。 */
export function useUserTimeZone() {
  const timeZone = useContext(UserTimeZoneContext)
  if (timeZone === null) {
    throw new Error("useUserTimeZone 必须在 UserPreferencesProvider 内使用")
  }
  return timeZone
}
