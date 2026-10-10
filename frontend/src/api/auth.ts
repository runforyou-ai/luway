/** 登录、注册、登出、首次安装与原生端服务器连接调用。 */
import { ApiError, clearToken, invoke, requestMeta, saveServerURL, storeToken } from "@/api/client"
import {
  APIVersion,
  ErrorKind,
  MinServerAPIVersion,
  SessionState,
  type Auth,
  type InstallationStatus,
  type InstallWorkspaceInput,
  type LoginInput,
  type RegisterInput,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"
import { i18n, resolveBrowserLanguage } from "@/i18n"
import { beginSessionBoundary } from "@/lib/resource-client"
import { resolveBrowserTimeZone } from "@/lib/time-zones"
import { syncLocale } from "@/platform/system"

/** 保存登录令牌并进入新的登录会话代次，原生端同步账号语言，返回登录账号。 */
function establishSession(auth: Auth) {
  const account = storeToken(auth)
  beginSessionBoundary()
  syncLocale(account.locale)
  return account
}

/** 用邮箱和密码登录并建立登录会话。 */
export async function login(input: LoginInput) {
  return establishSession(await ops.login(input))
}

/** 注册本地账号并建立登录会话，语言和时区取自当前浏览器。 */
export async function register(input: Omit<RegisterInput, "locale" | "timeZone">) {
  return establishSession(
    await ops.register({
      ...input,
      locale: resolveBrowserLanguage() as RegisterInput["locale"],
      timeZone: resolveBrowserTimeZone(),
    }),
  )
}

/** 退出登录：先清除本地令牌并进入新的登录会话代次，再用原令牌通知服务器。 */
export async function logout() {
  const meta = requestMeta()
  clearToken()
  beginSessionBoundary()
  await invoke({ method: "POST", path: "/auth/logout" }, meta)
}

/** 完成首次安装，建立平台管理员登录会话并返回创建的工作区。 */
export async function install(
  input: Omit<InstallWorkspaceInput, "locale" | "timeZone">,
) {
  const result = await ops.installWorkspace({
    ...input,
    locale: resolveBrowserLanguage() as InstallWorkspaceInput["locale"],
    timeZone: resolveBrowserTimeZone(),
  })
  establishSession(result.auth)
  return result.workspace
}

/** 检测服务器并返回安装状态；无法访问或不是本产品的服务器时抛出无法连接的错误。 */
export async function probeServer(serverURL: string): Promise<InstallationStatus> {
  const server = serverURL.trim().replace(/\/+$/, "")
  const status = await invoke<Partial<InstallationStatus>>({ method: "GET", path: "/installation/status" }, requestMeta(), undefined, server).catch(
    (error: unknown) => {
      console.warn("检测服务器失败", server, error)
      return undefined
    },
  )
  if (typeof status?.installed !== "boolean") {
    throw new ApiError(ErrorKind.Unavailable, "", i18n.t("connection:connectionError"))
  }
  return status as InstallationStatus
}

/** 检测并保存服务器地址：服务器尚未初始化或版本过旧时不保存；地址变化时清除原服务器的登录会话；本端接口版本过旧时保存地址后抛出需要升级客户端的错误。 */
export async function connectServer(serverURL: string) {
  const status = await probeServer(serverURL)
  const host = new URL(serverURL).host
  if (!status.installed) {
    throw new ApiError(ErrorKind.Invalid, "", i18n.t("connection:serverNotInstalled", { host }))
  }
  if (status.apiVersion < MinServerAPIVersion) {
    throw new ApiError(ErrorKind.Invalid, "", i18n.t("connection:serverOutdated", { host }))
  }
  saveServerURL(serverURL)
  if (status.minClientApiVersion > APIVersion) {
    throw new ApiError("", SessionState.Upgrade, i18n.t("connection:upgrade.description", { host }))
  }
}
