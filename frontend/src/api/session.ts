/** 读取启动入口、登录身份并提供会话状态路由。 */
import { invoke, requestMeta, serverURL } from "@/api/client"
import {
  APIVersion,
  MinServerAPIVersion,
  SessionState,
  type Brand,
  type InstallationStatus,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"
import { resolveAppPlatform } from "@/platform/app-platform"
import { syncLocale } from "@/platform/system"

/** 原生端已保存服务器仍进入连接页的原因：暂时无法访问、尚未完成首次安装或服务器接口版本过旧。 */
export type ConnectReason = "unreachable" | "not_installed" | "server_outdated"

/** 应用启动入口和界面使用的产品品牌；原生端已保存服务器仍进入连接页时 connectReason 说明原因。 */
export type Startup = {
  state: SessionState
  brand: Brand
  connectReason?: ConnectReason
}

/** 读取平台的安装状态和注册开关。 */
export const loadInstallationStatus = ops.installationStatus

/**
 * 读取启动入口：Web 端按平台是否完成首次安装进入初始化页或应用；
 * 原生端检测已保存服务器的连通、安装状态与接口版本，进入连接页或升级页时使用构建品牌，就绪后使用服务器下发的品牌。
 */
export async function loadStartup(): Promise<Startup> {
  if (resolveAppPlatform() === "web") {
    const status = await ops.installationStatus()
    return { state: status.installed ? SessionState.Ready : SessionState.Setup, brand: status.brand }
  }
  const build: Brand = { names: __BUILD_BRAND__.names, sdkName: __BUILD_BRAND__.sdkName, linkScheme: __BUILD_BRAND__.linkScheme }
  const server = serverURL()
  if (!server) return { state: SessionState.Connect, brand: build }
  let status: InstallationStatus
  try {
    status = await invoke<InstallationStatus>({ method: "GET", path: "/installation/status" }, requestMeta(), undefined, server)
  } catch (error) {
    console.warn("读取服务器安装状态失败，进入连接页", server, error)
    return { state: SessionState.Connect, brand: build, connectReason: "unreachable" }
  }
  if (!status.installed) return { state: SessionState.Connect, brand: build, connectReason: "not_installed" }
  if (status.apiVersion < MinServerAPIVersion) return { state: SessionState.Connect, brand: build, connectReason: "server_outdated" }
  if (status.minClientApiVersion > APIVersion) return { state: SessionState.Upgrade, brand: build }
  return { state: SessionState.Ready, brand: status.brand }
}

/** 读取当前账号在当前工作区中的成员身份，原生端同步成员语言。 */
export async function loadIdentity(signal?: AbortSignal) {
  const identity = await ops.loadIdentity(signal)
  syncLocale(identity.user.locale)
  return identity
}

/** 读取当前用户可见会话与身份资料的同步探针值。 */
export const getSyncHeads = ops.getSyncHeads

/** 将会话状态映射为根路径下的账号级路由。 */
export function sessionPath(state: string) {
  switch (state) {
    case SessionState.Login:
      return "/login"
    case SessionState.Setup:
      return resolveAppPlatform() === "web" ? "/setup" : "/connect"
    case SessionState.Connect:
      return "/connect"
    case SessionState.Workspace:
      return "/workspaces"
    case SessionState.Upgrade:
      return "/upgrade"
    default:
      return null
  }
}
