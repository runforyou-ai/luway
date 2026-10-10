/** 桌面端主窗口在工作区中保持本机电脑的注册。 */
import { useEffect, useState } from "react"

import { loadAccount } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { resolveDesktopOS } from "@/platform/app-platform"
import { isDesktopMainWindow } from "@/platform/desktop-window"
import { syncComputerRegistrations } from "@/platform/local-computer"

/** 同步失败后重试的间隔。 */
const retryDelayMs = 30_000

/** 工作区列表变化时为账号所在的工作区注册这台电脑并删除账号已不属于的工作区的注册，失败时按间隔重试；只在桌面端主窗口运行。 */
export function useLocalComputerRegistration(workspaceIDs: string[] | undefined) {
  const desktop = resolveDesktopOS() !== null
  const account = useResource(resourceKeys.account(), (signal) => loadAccount(signal), { enabled: desktop })
  const accountID = account.data?.id
  const key = workspaceIDs?.join(",")
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!desktop || !accountID || key === undefined) return
    let stale = false
    let retry: ReturnType<typeof setTimeout> | undefined
    void (async () => {
      if (!(await isDesktopMainWindow()) || stale) return
      try {
        await syncComputerRegistrations(accountID, key ? key.split(",") : [])
      } catch (error) {
        console.warn("同步电脑注册失败", error)
        if (!stale) retry = setTimeout(() => setAttempt((current) => current + 1), retryDelayMs)
      }
    })()
    return () => {
      stale = true
      clearTimeout(retry)
    }
  }, [desktop, accountID, key, attempt])
}
