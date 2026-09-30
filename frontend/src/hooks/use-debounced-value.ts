/** 输入值防抖。 */
import { useEffect, useState } from "react"

/** 返回停止变化 delay 毫秒后的值；immediate 为 true 时直接返回当前值，结束后从当前值开始防抖。 */
export function useDebouncedValue<T>(value: T, delay: number, immediate = false) {
  const [debounced, setDebounced] = useState(value)
  if (immediate && !Object.is(debounced, value)) setDebounced(value)
  useEffect(() => {
    if (immediate) return
    const timer = window.setTimeout(() => setDebounced(value), delay)
    return () => window.clearTimeout(timer)
  }, [value, delay, immediate])
  return immediate ? value : debounced
}
