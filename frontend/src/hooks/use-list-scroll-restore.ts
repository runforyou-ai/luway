/** 记录并恢复列表页滚动容器的位置。 */
import { useLayoutEffect, useRef, type UIEvent } from "react"

const listScrollPositions = new Map<string, number>()

/** 按滚动键记录容器位置；内容就绪后每个键恢复一次，恢复前的滚动不记录。 */
export function useListScrollRestore(key: string, ready: boolean) {
  const ref = useRef<HTMLDivElement>(null)
  const restoredKey = useRef("")

  // 当前键的内容就绪后恢复上次离开时的位置。
  useLayoutEffect(() => {
    if (!ready || !ref.current || restoredKey.current === key) return
    ref.current.scrollTop = listScrollPositions.get(key) ?? 0
    restoredKey.current = key
  }, [key, ready])

  /** 记录当前键已恢复后的滚动位置。 */
  function onScroll(event: UIEvent<HTMLDivElement>) {
    if (restoredKey.current === key)
      listScrollPositions.set(key, event.currentTarget.scrollTop)
  }

  return { ref, onScroll }
}
