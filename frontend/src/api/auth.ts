/** 登录、注册、登出、首次安装、服务器地址和连接链接调用。 */
import { Events } from "@wailsio/runtime"

import {
  ConnectServer,
  InstallWorkspace,
  Login,
  Logout,
  ProbeServer,
  Register,
  ServerURL,
  TakeOpenedServerLink,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import type {
  Auth,
  InstallWorkspaceInput,
  LoginInput,
  RegisterInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import {
  bind,
  clearWebToken,
  invoke,
  requestMeta,
  storeWebToken,
} from "@/api/client"
import { resolveBrowserLanguage } from "@/i18n"
import { beginSessionBoundary } from "@/lib/resource-client"
import { resolveBrowserTimeZone } from "@/lib/time-zones"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 读取已保存的服务器地址。 */
export const getServerURL = bind(ServerURL)

// 与 internal/appservice/types.go 中的 ServerLinkOpenedEventName 保持一致。
const serverLinkOpenedEventName = "app:server-link:opened"

/** 读取并清除原生端最近一次被连接链接唤起时携带的部署地址。 */
export const takeOpenedServerLink = bind(TakeOpenedServerLink)

/** 订阅原生端被连接链接唤起，返回取消订阅函数。 */
export function onServerLinkOpened(listener: () => void) {
  return Events.On(serverLinkOpenedEventName, () => listener())
}

/** 建立当前平台的登录会话：Web 端保存令牌，原生端由平台层保存；之后进入新的登录会话代次并返回登录账号。 */
function establishSession(auth: Auth) {
  const account = resolveAppPlatform() === "web" ? storeWebToken(auth) : auth.account
  beginSessionBoundary()
  return account
}

/** 用邮箱和密码登录并建立当前平台会话。 */
export async function login(input: LoginInput) {
  return establishSession(await invoke((meta) => Login(meta, input)))
}

/** 注册本地账号并建立当前平台会话，语言和时区取自当前浏览器。 */
export async function register(input: Omit<RegisterInput, "locale" | "timeZone">) {
  return establishSession(
    await invoke((meta) =>
      Register(meta, {
        ...input,
        locale: resolveBrowserLanguage() as RegisterInput["locale"],
        timeZone: resolveBrowserTimeZone(),
      }),
    ),
  )
}

/** 退出登录：先清除本地令牌并进入新的登录会话代次，再用原令牌通知服务器。 */
export async function logout() {
  const meta = requestMeta()
  clearWebToken()
  beginSessionBoundary()
  await invoke(Logout, meta)
}

/** 完成首次安装，建立平台管理员登录会话并返回创建的工作区。 */
export async function install(
  input: Omit<InstallWorkspaceInput, "locale" | "timeZone">,
) {
  const result = await invoke((meta) =>
    InstallWorkspace(meta, {
      ...input,
      locale: resolveBrowserLanguage() as InstallWorkspaceInput["locale"],
      timeZone: resolveBrowserTimeZone(),
    }),
  )
  establishSession(result.auth)
  return result.workspace
}

/** 检测服务器并返回安装状态。 */
export const probeServer = bind(ProbeServer)

/** 进入新的登录会话代次后验证并保存服务器地址。 */
export async function connectServer(serverURL: string) {
  beginSessionBoundary()
  await invoke((meta) => ConnectServer(meta, serverURL))
}
