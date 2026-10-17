/** 同步当前用户语言并提供日期时间显示时区。 */
import { createContext, useContext, useEffect, type ReactNode } from "react"

import type { Account, CurrentUser } from "@/api"
import { changeAppLanguage } from "@/i18n"

const UserTimeZoneContext = createContext<string | null>(null)

/** 同步界面语言与日期时间显示时区所需的最小账号字段；用户端成员与平台后台账号均可提供。 */
export type UserPreferencesSource = Pick<CurrentUser, "locale" | "timeZone"> | Pick<Account, "locale" | "timeZone">

/** 同步用户语言并向子页面提供用户时区。 */
export function UserPreferencesProvider({
  user,
  children,
}: {
  user: UserPreferencesSource
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
