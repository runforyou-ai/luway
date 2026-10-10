/** 列表与编辑、详情页之间的来源地址：列表生成携带来源的链接，子页面读取来源用于返回、取消、保存后跳转和记录不存在时的返回。 */
import { useCallback, useEffect } from "react"
import { useLocation, useNavigate, useSearchParams } from "react-router"

const returnToParameter = "returnTo"

/** 返回携带当前页面来源的链接，并按需移除弹窗状态参数。 */
export function useReturnLink({ omitSearchParams = [] }: { omitSearchParams?: string[] } = {}) {
  const location = useLocation()
  const searchParams = new URLSearchParams(location.search)
  for (const name of omitSearchParams) searchParams.delete(name)
  const filteredSearch = searchParams.toString()
  const search = omitSearchParams.length ? (filteredSearch ? `?${filteredSearch}` : "") : location.search
  const current = location.pathname + search
  return useCallback(
    (path: string) => {
      const separator = path.includes("?") ? "&" : "?"
      return `${path}${separator}${returnToParameter}=${encodeURIComponent(current)}`
    },
    [current],
  )
}

/** 读取来源列表地址：来源路径与 fallback 相同或满足 allowed 时保留其查询条件，否则使用 fallback；notFound 为真时按 logFields 记录日志并替换历史回到来源。 */
export function useReturnTo(
  fallback: string,
  {
    allowed,
    notFound = false,
    logFields,
  }: { allowed?: (path: string) => boolean; notFound?: boolean; logFields?: Record<string, string> } = {},
) {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const location = useLocation()
  const parameter = searchParams.get(returnToParameter) ?? ""
  const path = parameter.split("?")[0]
  const accepted = path !== "" && (path === fallback.split("?")[0] || Boolean(allowed?.(path)))
  const returnTo = accepted ? parameter : fallback

  const leave = useCallback(
    (options?: { replace?: boolean }) => navigate(returnTo, options),
    [navigate, returnTo],
  )

  const logFieldsKey = JSON.stringify(logFields ?? {})

  // 记录不存在时替换当前历史回到来源列表。
  useEffect(() => {
    if (!notFound) return
    console.warn("记录不存在，返回来源列表", { path: location.pathname, ...JSON.parse(logFieldsKey) })
    navigate(returnTo, { replace: true })
  }, [location.pathname, logFieldsKey, navigate, notFound, returnTo])

  return { returnTo, leave }
}
