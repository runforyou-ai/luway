/** 注入请求认证信息，并把应用服务错误转换为前端错误。 */
import { CancelError, type CancellablePromise } from "@wailsio/runtime"

import {
  Locale,
  type Auth,
  type RequestMeta,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import type { NonNullArrays } from "@/api/normalize"
import {
  currentSessionGeneration,
  settleInSessionGeneration,
} from "@/api/session-scope"
import { i18n } from "@/i18n"
import { fallbackLanguage } from "@/i18n/resources"
import { markSessionExpired } from "@/lib/login-return"
import { beginSessionBoundary } from "@/lib/resource-client"
import { resolveAppPlatform } from "@/platform/app-platform"

const tokenStorageKey = "app.token"
const androidErrorMarker = "\n__APP_API_ERROR_V1__:"

type StoredToken = Pick<Auth, "token" | "expiresAt">

type ErrorCause = {
  kind?: string
  state?: string
  message: string
  fields?: Record<string, string>
  reason?: string
}

/** 应用服务返回的结构化业务错误。 */
export class ApiError extends Error {
  readonly kind: string
  readonly state: string
  readonly fields: Record<string, string>
  readonly reason: string

  /** 创建结构化业务错误。 */
  constructor(
    kind: string,
    state: string,
    message: string,
    fields: Record<string, string> = {},
    reason = "",
  ) {
    super(message)
    this.name = "ApiError"
    this.kind = kind
    this.state = state
    this.fields = fields
    this.reason = reason
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

// 当前页面所在工作区的编号，工作区级请求经请求信息带给服务端；账号级页面为空。
let requestWorkspaceID = ""

/** 设置后续请求的目标工作区，进入或离开工作区时调用。 */
export function setRequestWorkspace(workspaceID: string) {
  requestWorkspaceID = workspaceID
}

/** 以指定工作区为目标发起一次调用，用于后台读取其他工作区的数据：request 须在第一次等待之前同步发出请求，返回后即恢复当前工作区。 */
export function callInWorkspace<T>(workspaceID: string, request: () => Promise<T>): Promise<T> {
  const current = requestWorkspaceID
  requestWorkspaceID = workspaceID
  try {
    return request()
  } finally {
    requestWorkspaceID = current
  }
}

// Web 端当前登录会话代次归属的令牌原文，本页写入令牌时同步更新。
let sessionToken = resolveAppPlatform() === "web" ? (readStoredToken()?.token ?? "") : ""

if (resolveAppPlatform() === "web") {
  // 其他标签页改动令牌时本页进入新的登录会话代次。
  window.addEventListener("storage", (event) => {
    if (event.key === tokenStorageKey || event.key === null) {
      adoptForeignWebToken()
    }
  })
}

/**
 * 注入认证和语言后调用应用服务，卸载时丢弃过期结果；只交付发起时代次仍为当前代次的结果。
 * 结果按服务端保证的非空切片声明类型。
 */
export function call<T>(
  operation: (meta: RequestMeta) => CancellablePromise<T>,
  signal?: AbortSignal,
): Promise<NonNullArrays<T>> {
  const meta = sessionRequestMeta()
  // 令牌已被其他标签页改动时本次调用属于旧会话，保持挂起。
  if (!meta) {
    return new Promise(() => {})
  }
  return settleInSessionGeneration(currentSessionGeneration(), invoke(operation, meta, signal))
}

/** 使用给定请求信息调用应用服务并转换错误，结果不受登录会话代次约束，供会话边界操作使用。 */
export async function invoke<T>(
  operation: (meta: RequestMeta) => CancellablePromise<T>,
  meta: RequestMeta = requestMeta(),
  signal?: AbortSignal,
): Promise<NonNullArrays<T>> {
  try {
    const result = await operation(meta)
    if (signal?.aborted) {
      throw abortError()
    }
    return result as NonNullArrays<T>
  } catch (error) {
    if (signal?.aborted) {
      throw abortError()
    }
    throw normalizeError(error)
  }
}

/** 组装当前请求的令牌、目标工作区和语言，Web 端读取时清除已过期令牌并让登录页提示登录已过期。 */
export function requestMeta(): RequestMeta {
  let token = ""
  if (resolveAppPlatform() === "web") {
    const stored = readStoredToken()
    if (stored && Date.parse(stored.expiresAt) <= Date.now()) {
      markSessionExpired()
      clearWebToken()
    } else {
      token = stored?.token ?? ""
    }
  }
  return {
    token,
    workspaceId: requestWorkspaceID,
    locale:
      (i18n.resolvedLanguage ?? fallbackLanguage) === "en-US"
        ? Locale.LocaleEnglishUnitedStates
        : Locale.LocaleChineseSimplified,
  }
}

/** 返回本代次请求信息；Web 端令牌已被其他标签页改动时进入新的登录会话代次并返回 undefined。 */
export function sessionRequestMeta(): RequestMeta | undefined {
  if (resolveAppPlatform() === "web" && adoptForeignWebToken()) {
    return undefined
  }
  return requestMeta()
}

/** 采用其他标签页写入的令牌并进入新的登录会话代次，令牌未变化时返回 false。 */
function adoptForeignWebToken() {
  const token = readStoredToken()?.token ?? ""
  if (token === sessionToken) {
    return false
  }
  sessionToken = token
  console.info("登录令牌已被其他页面改动，进入新的登录会话")
  beginSessionBoundary()
  return true
}

/** 读取 Web 端本地保存的令牌，内容无法解析时视为未登录。 */
function readStoredToken(): StoredToken | undefined {
  const value = window.localStorage.getItem(tokenStorageKey)
  if (!value) {
    return undefined
  }
  try {
    return JSON.parse(value) as StoredToken
  } catch {
    return undefined
  }
}

/** 返回调用方已离开后应忽略的中止错误。 */
function abortError() {
  return new DOMException("The operation was aborted.", "AbortError")
}

/** 把应用服务方法包装成自动注入认证信息的前端调用。 */
export function bind<A extends unknown[], R>(
  fn: (meta: RequestMeta, ...args: A) => CancellablePromise<R>,
) {
  return (...args: [...A, AbortSignal?]) => {
    const last = args[args.length - 1]
    const signal = last instanceof AbortSignal ? last : undefined
    const fnArgs = (
      (signal || last === undefined) && args.length > 0
        ? args.slice(0, -1)
        : args
    ) as A
    return call((meta) => fn(meta, ...fnArgs), signal)
  }
}

/** 保存 Web 端登录令牌并返回登录账号。 */
export function storeWebToken(auth: Auth) {
  window.localStorage.setItem(
    tokenStorageKey,
    JSON.stringify({ token: auth.token, expiresAt: auth.expiresAt }),
  )
  sessionToken = auth.token
  return auth.account
}

/** 返回 Web 端当前是否持有登录令牌。 */
export function hasWebToken() {
  return sessionToken !== ""
}

/** 清除 Web 端登录令牌。 */
export function clearWebToken() {
  if (resolveAppPlatform() === "web") {
    window.localStorage.removeItem(tokenStorageKey)
    sessionToken = ""
  }
}

/** 把 HTTP 响应中的业务错误体转换为前端错误，错误体不可识别时返回通用错误。 */
export function responseError(status: number, body: unknown) {
  const cause =
    typeof body === "object" && body !== null
      ? (body as { error?: unknown }).error
      : undefined
  return isErrorCause(cause)
    ? apiErrorFromCause(cause)
    : new Error(`request failed with status ${status}`)
}

/** 把应用服务异常转换为前端错误。 */
export function normalizeError(error: unknown) {
  if (error instanceof ApiError) return error
  if (error instanceof Error) {
    const cause = (error as Error & { cause?: unknown }).cause
    if (error instanceof CancelError && cause instanceof Error) return cause
    if (isErrorCause(cause)) {
      return apiErrorFromCause(cause)
    }
    const androidCause = parseAndroidErrorCause(error.message)
    if (androidCause) return apiErrorFromCause(androidCause)
    return error
  }
  return new ApiError("failed", "", "Request failed")
}

/** 从结构化错误原因创建统一的前端业务错误。 */
function apiErrorFromCause(cause: ErrorCause) {
  return new ApiError(
    cause.kind ?? "",
    cause.state ?? "",
    cause.message,
    cause.fields ?? {},
    cause.reason ?? "",
  )
}

/** 从 Android 错误文本恢复 wailsapp/wails#6053 未传递的结构化原因。 */
function parseAndroidErrorCause(message: string) {
  const markerIndex = message.lastIndexOf(androidErrorMarker)
  if (markerIndex < 0) return undefined
  const encoded = message.slice(markerIndex + androidErrorMarker.length)
  try {
    const bytes = Uint8Array.from(globalThis.atob(encoded), (character) =>
      character.charCodeAt(0),
    )
    const cause: unknown = JSON.parse(new TextDecoder().decode(bytes))
    return isErrorCause(cause) ? cause : undefined
  } catch {
    return undefined
  }
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
