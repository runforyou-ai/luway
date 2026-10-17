/** 读取并更新报表页地址参数：值等于 defaults 中的默认值时从地址中移除，更新时未给出的 openItems 参数随之关闭。 */
import { useCallback } from "react"
import { useSearchParams } from "react-router"

export function useReportSearchParams<K extends string>(
  defaults: Record<K, string>,
  openItems: readonly NoInfer<K>[],
) {
  const [searchParams, setSearchParams] = useSearchParams()
  const setParameters = useCallback(
    (changes: Partial<Record<K, string>>) => {
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current)
          for (const name of openItems) if (changes[name] === undefined) next.delete(name)
          for (const [name, value] of Object.entries(changes) as [K, string | undefined][]) {
            if (value === undefined) continue
            if (value === defaults[name]) next.delete(name)
            else next.set(name, value)
          }
          return next
        },
        { replace: true },
      )
    },
    [defaults, openItems, setSearchParams],
  )
  return [searchParams, setParameters] as const
}
