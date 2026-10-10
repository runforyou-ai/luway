/** 复制文本到剪贴板，并在短时间内标记刚复制的目标。 */
import { useEffect, useState } from "react"

/** 返回刚复制的目标与复制方法；复制成功后 2 秒内 copied 为该目标，复制失败时返回 false。 */
export function useCopyFeedback<K extends string>() {
  const [copied, setCopied] = useState<K | null>(null)

  useEffect(() => {
    if (copied === null) return
    const timeout = window.setTimeout(() => setCopied(null), 2000)
    return () => window.clearTimeout(timeout)
  }, [copied])

  /** 复制文本并标记目标。 */
  async function copy(value: string, target: K) {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(target)
      return true
    } catch (error) {
      console.warn("复制到剪贴板失败", error)
      setCopied(null)
      return false
    }
  }

  return { copied, copy }
}
