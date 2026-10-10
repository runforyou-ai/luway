/** 展示资源的首次加载、失败重试和就绪内容。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { isApiError } from "@/api"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { apiErrorMessage } from "@/lib/form-errors"

/** useResource 返回结果中判断读取状态所需的部分。 */
export type ResourceState = {
  data: unknown
  error: unknown
  retrying: boolean
  refresh: () => unknown
}

/**
 * 汇总页面所需资源的读取状态：有数据的资源视为就绪，后台刷新失败不替换已有内容；
 * 尚无数据时，读取失败且不在重试中为 error，其余为 loading。只传入已启用的资源。
 */
export function resourceStatus(resources: ResourceState | readonly ResourceState[]) {
  const list = Array.isArray(resources) ? resources : [resources as ResourceState]
  const pending = list.filter((resource) => resource.data === undefined)
  const failed = pending.filter((resource) => resource.error && !resource.retrying)
  return {
    status: failed.length > 0 ? "error" : pending.length > 0 ? "loading" : "ready",
    failed,
  } as const
}

/** 为管理页面保留一致的加载与重试区域；服务端给出的错误说明优先于 errorMessage。children 在就绪前也会先求值，其中读取结果用可选链访问。 */
export function ResourceContent({
  resources,
  errorMessage,
  children,
}: {
  resources: ResourceState | readonly ResourceState[]
  errorMessage: string
  children: ReactNode
}) {
  const { t } = useTranslation("common")
  const { status, failed } = resourceStatus(resources)
  if (status === "loading")
    return (
      <LoadingIndicator className="min-h-48 justify-center">
        {t("status.loading")}
      </LoadingIndicator>
    )
  if (status === "error") {
    const apiError = failed.map((resource) => resource.error).find(isApiError)
    return (
      <div className="flex min-h-48 flex-col items-center justify-center text-center">
        <p className="text-sm text-muted-foreground">
          {apiError ? apiErrorMessage(apiError) : errorMessage}
        </p>
        <Button
          type="button"
          className="mt-4"
          variant="outline"
          onClick={() => {
            // 一次重试所有读取失败的资源。
            for (const resource of failed) void resource.refresh()
          }}
        >
          {t("actions.retry")}
        </Button>
      </div>
    )
  }
  return children
}
