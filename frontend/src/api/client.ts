/** 连接服务端：经凭据存储保存服务器地址与登录令牌，注入认证、工作区与语言后请求服务端接口，并把错误转换为前端错误。 */
import { CancelError } from "@wailsio/runtime"

import {
  APIVersion,
  ClientAPIVersionHeader,
  Locale,
  WorkspaceHeader,
  type Auth,
} from "@/api/generated/contract"
import { currentSessionGeneration, requestWorkspace, settleInSessionGeneration } from "@/api/session-scope"
import { i18n } from "@/i18n"
import { fallbackLanguage } from "@/i18n/resources"
import { markSessionExpired } from "@/lib/login-return"
import { beginSessionBoundary } from "@/lib/resource-client"
import { resolveAppPlatform } from "@/platform/app-platform"
import { credentials } from "@/platform/credentials"

// 服务端返回的本地存储文件相对路径前缀，原生端补全为服务器地址下的完整地址。
const localFilePrefix = "/storage/"

type ErrorCause = {
  kind?: string
  state?: string
  message: string
  fields?: Record<string, string>
  reason?: string
}

/** 一次请求携带的登录令牌、目标工作区与界面语言。 */
export type RequestMeta = {
  token: string
  workspaceId: string
  locale: Locale
}

type QueryValue = string | number | boolean | readonly string[] | null | undefined

/** 一次服务端接口调用：路径相对服务端 /api，查询参数与 JSON 请求体可选。 */
export type RequestSpec = {
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE"
  path: string
  query?: Record<string, QueryValue>
  body?: unknown
}

/** 应用服务返回的结构化业务错误。 */
export class ApiError extends Error {
  readonly kind: string
  readonly state: string
  readonly fields: Record<string, string>
  readonly reason: string
  /** 服务端响应头 X-Trace-ID 给出的串联编号，本机能力错误与无法连接服务器时为空。 */
  readonly traceId: string

  /** 创建结构化业务错误。 */
  constructor(
    kind: string,
    state: string,
    message: string,
    fields: Record<string, string> = {},
    reason = "",
    traceId = "",
  ) {
    super(message)
    this.name = "ApiError"
    this.kind = kind
    this.state = state
    this.fields = fields
    this.reason = reason
    this.traceId = traceId
  }
}

/** 判断是否为应用服务业务错误。 */
export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError
}

/** 判断是否为资源不存在错误。 */
export function isNotFoundApiError(error: unknown): error is ApiError {
  return isApiError(error) && error.kind === "not_found"
}

/** 返回当前连接的服务器地址：Web 端为页面来源，原生端为已保存的地址，尚未保存时为构建品牌的部署地址。 */
export const serverURL = credentials.serverURL

/** 保存原生端连接的服务器地址；地址变化时清除原服务器的登录令牌并进入新的登录会话代次。 */
export function saveServerURL(address: string) {
  const normalized = address.trim().replace(/\/+$/, "")
  if (normalized === serverURL()) return
  credentials.saveServerURL(normalized)
  credentials.removeToken()
  sessionToken = ""
  beginSessionBoundary()
}

// 本页当前登录会话代次归属的令牌原文，本页写入令牌时同步更新。
let sessionToken = credentials.readToken()?.token ?? ""

// 同源的其他页面或窗口改动令牌时本页进入新的登录会话代次。
credentials.onTokenChanged(adoptForeignToken)

/**
 * 在当前登录会话代次中调用服务端接口，只交付发起时代次仍为当前代次的结果；
 * 令牌已被其他页面改动时本次调用属于旧会话，保持挂起。
 */
export function call<T>(spec: RequestSpec, signal?: AbortSignal): Promise<T> {
  const meta = sessionRequestMeta()
  if (!meta) {
    return new Promise(() => {})
  }
  return settleInSessionGeneration(currentSessionGeneration(), invoke<T>(spec, meta, signal))
}

/** 使用给定请求信息调用服务端接口，结果不受登录会话代次约束，供会话边界操作使用；server 指定时请求该服务器。 */
export async function invoke<T>(
  spec: RequestSpec,
  meta: RequestMeta = requestMeta(),
  signal?: AbortSignal,
  server = serverURL(),
): Promise<T> {
  const query = new URLSearchParams()
  for (const [name, value] of Object.entries(spec.query ?? {})) {
    // 空值、false 与非正整数等同于未提供该查询参数。
    if (Array.isArray(value)) {
      for (const item of value) query.append(name, item)
    } else if (value !== undefined && value !== null && value !== "" && value !== false && !(typeof value === "number" && value <= 0)) {
      query.append(name, String(value))
    }
  }
  const headers = requestHeaders(meta, true)
  if (spec.body !== undefined) headers["Content-Type"] = "application/json"
  const search = query.size > 0 ? `?${query.toString()}` : ""
  let response: Response
  try {
    response = await fetch(`${server}/api${spec.path}${search}`, {
      method: spec.method,
      headers,
      body: spec.body === undefined ? undefined : JSON.stringify(spec.body),
      signal,
    })
  } catch (error) {
    if (signal?.aborted) throw error
    throw new ApiError("unavailable", "", i18n.t("common:errors.network"))
  }
  if (!response.ok) {
    const body: unknown = await response.json().catch(() => undefined)
    throw responseError(response, body)
  }
  if (response.status === 204) {
    return undefined as T
  }
  const result: unknown = await response.json()
  if (resolveAppPlatform() !== "web") {
    resolveFileURLs(result, server)
  }
  return result as T
}

/** 把结果中字段名以 url 结尾、值为本地存储相对路径的字段就地补全为服务器地址下的完整地址。 */
function resolveFileURLs(value: unknown, server: string) {
  if (Array.isArray(value)) {
    for (const item of value) resolveFileURLs(item, server)
    return
  }
  if (typeof value !== "object" || value === null) return
  const record = value as Record<string, unknown>
  for (const [key, field] of Object.entries(record)) {
    if (typeof field === "string" && key.toLowerCase().endsWith("url") && field.startsWith(localFilePrefix)) {
      record[key] = server + field
    } else {
      resolveFileURLs(field, server)
    }
  }
}

/** 组装当前请求的令牌、目标工作区和语言，读取时清除已过期令牌并让登录页提示登录已过期。 */
export function requestMeta(): RequestMeta {
  let token = ""
  const stored = credentials.readToken()
  if (stored && Date.parse(stored.expiresAt) <= Date.now()) {
    markSessionExpired()
    clearToken()
  } else {
    token = stored?.token ?? ""
  }
  return {
    token,
    workspaceId: requestWorkspace(),
    locale:
      (i18n.resolvedLanguage ?? fallbackLanguage) === "en-US"
        ? Locale.EnglishUnitedStates
        : Locale.ChineseSimplified,
  }
}

/** 返回本代次请求信息；令牌已被其他页面改动时进入新的登录会话代次并返回 undefined。 */
export function sessionRequestMeta(): RequestMeta | undefined {
  if (adoptForeignToken()) {
    return undefined
  }
  return requestMeta()
}

/** 返回请求服务端时携带的请求头，withWorkspace 为 false 时不携带目标工作区；原生端另外声明本端接口版本。 */
export function requestHeaders(meta: RequestMeta, withWorkspace: boolean): Record<string, string> {
  const headers: Record<string, string> = { Accept: "application/json", "Accept-Language": meta.locale }
  if (meta.token) headers.Authorization = `Bearer ${meta.token}`
  if (withWorkspace && meta.workspaceId) headers[WorkspaceHeader] = meta.workspaceId
  if (resolveAppPlatform() !== "web") headers[ClientAPIVersionHeader] = String(APIVersion)
  return headers
}

/** 采用其他页面写入的令牌并进入新的登录会话代次，令牌未变化时返回 false。 */
function adoptForeignToken() {
  const token = credentials.readToken()?.token ?? ""
  if (token === sessionToken) {
    return false
  }
  sessionToken = token
  console.info("登录令牌已被其他页面改动，进入新的登录会话")
  beginSessionBoundary()
  return true
}

/** 保存登录令牌并返回登录账号。 */
export function storeToken(auth: Auth) {
  credentials.writeToken(auth)
  sessionToken = auth.token
  return auth.account
}

/** 返回当前是否持有登录令牌。 */
export function hasToken() {
  return sessionToken !== ""
}

/** 清除登录令牌。 */
export function clearToken() {
  credentials.removeToken()
  sessionToken = ""
}

/** 把失败的 HTTP 响应转换为前端错误并带上响应头中的串联编号，错误体不可识别时按服务端内部错误处理。 */
export function responseError(response: Response, body: unknown) {
  const traceId = response.headers.get("X-Trace-ID") ?? ""
  const cause =
    typeof body === "object" && body !== null
      ? (body as { error?: unknown }).error
      : undefined
  return isErrorCause(cause)
    ? apiErrorFromCause(cause, traceId)
    : new ApiError("failed", "", i18n.t("common:errors.internal"), {}, "", traceId)
}

/** 把本机能力调用的异常转换为前端错误。 */
export function normalizeError(error: unknown) {
  if (error instanceof ApiError) return error
  if (error instanceof Error) {
    const cause = (error as Error & { cause?: unknown }).cause
    if (error instanceof CancelError && cause instanceof Error) return cause
    if (isErrorCause(cause)) {
      return apiErrorFromCause(cause)
    }
    return error
  }
  return new ApiError("failed", "", "Request failed")
}

/** 从结构化错误原因创建统一的前端业务错误，traceId 为服务端响应给出的串联编号。 */
function apiErrorFromCause(cause: ErrorCause, traceId = "") {
  return new ApiError(
    cause.kind ?? "",
    cause.state ?? "",
    cause.message,
    cause.fields ?? {},
    cause.reason ?? "",
    traceId,
  )
}

/** 判断异常原因是否为结构化业务错误。 */
function isErrorCause(value: unknown): value is ErrorCause {
  if (typeof value !== "object" || value === null) return false
  const cause = value as Partial<ErrorCause>
  return (
    typeof cause.message === "string" &&
    ("kind" in cause || "state" in cause || "fields" in cause)
  )
}
