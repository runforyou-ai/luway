/** 提供页面布局使用的响应式视口断点。 */
import * as React from "react"

const NARROW_VIEWPORT_BREAKPOINT = 768
const NARROW_VIEWPORT_QUERY = `(max-width: ${NARROW_VIEWPORT_BREAKPOINT - 1}px)`
const WIDE_VIEWPORT_BREAKPOINT = 1536
const WIDE_VIEWPORT_QUERY = `(min-width: ${WIDE_VIEWPORT_BREAKPOINT}px)`

/** 监听指定媒体查询是否匹配。 */
function useViewportMatch(query: string) {
  const [matches, setMatches] = React.useState(
    () => window.matchMedia(query).matches,
  )

  React.useEffect(() => {
    const mediaQuery = window.matchMedia(query)
    const updateViewportMatch = (event: MediaQueryListEvent) =>
      setMatches(event.matches)

    mediaQuery.addEventListener("change", updateViewportMatch)
    return () => mediaQuery.removeEventListener("change", updateViewportMatch)
  }, [query])

  return matches
}

/** 监听窄视口断点是否匹配。 */
export function useIsNarrowViewport() {
  return useViewportMatch(NARROW_VIEWPORT_QUERY)
}

/** 监听会话上下文栏常驻断点是否匹配。 */
export function useIsWideViewport() {
  return useViewportMatch(WIDE_VIEWPORT_QUERY)
}
