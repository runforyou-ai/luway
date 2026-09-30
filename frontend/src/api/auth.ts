/** 登录、注册、官方账号登录、登出、首次安装、服务器地址和连接链接调用。 */
import { Events } from "@wailsio/runtime"

import {
  CompleteOfficialLogin,
  ConnectServer,
  InstallWorkspace,
  Login,
  Logout,
  ProbeServer,
  Register,
  ServerURL,
  StartOfficialLogin,
  TakeOpenedServerLink,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import type {
  Auth,
  InstallWorkspaceInput,
  LoginInput,
  RegisterInput,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import {
  bind,
  clearWebToken,
  invoke,
  requestMeta,
  storeWebToken,
} from "@/api/client"
import { resolveBrowserLanguage } from "@/i18n"
import { beginSessionBoundary } from "@/lib/resource-client"
import { randomURLSafeString, s256Challenge } from "@/lib/pkce"
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

const officialLoginStoragePrefix = "app.officialLogin."

/** 官方账号授权跳转期间按 state 保存的登录尝试与 PKCE verifier。 */
type PendingOfficialLogin = {
  attemptId: string
  codeVerifier: string
}

/** 官方账号登录回调不属于当前浏览器会话发起的登录尝试。 */
export class OfficialLoginStateError extends Error {}

/** 发起官方账号登录，在当前浏览器会话保存 state 对应的登录尝试与 verifier，返回授权地址。 */
export async function startOfficialLogin() {
  const state = randomURLSafeString(24)
  const codeVerifier = randomURLSafeString(48)
  const codeChallenge = await s256Challenge(codeVerifier)
  const started = await invoke((meta) =>
    StartOfficialLogin(meta, { state, nonce: randomURLSafeString(24), codeChallenge }),
  )
  const pending: PendingOfficialLogin = { attemptId: started.attemptId, codeVerifier }
  sessionStorage.setItem(officialLoginStoragePrefix + state, JSON.stringify(pending))
  return started.authorizationUrl
}

/** 用回调中的授权码完成官方账号登录；发起页面仍有效时建立当前平台会话并进入新的登录会话代次，否则返回 null。 */
export async function completeOfficialLogin(state: string, code: string, isCurrent: () => boolean) {
  const key = officialLoginStoragePrefix + state
  const stored = sessionStorage.getItem(key)
  sessionStorage.removeItem(key)
  if (!stored) throw new OfficialLoginStateError()
  const pending = JSON.parse(stored) as PendingOfficialLogin
  const auth = await invoke((meta) =>
    CompleteOfficialLogin(meta, { attemptId: pending.attemptId, code, codeVerifier: pending.codeVerifier }),
  )
  if (!isCurrent()) return null
  return establishSession(auth)
}

/** 退出登录：先清除本地令牌并进入新的登录会话代次，再用原令牌通知服务器。 */
export async function logout() {
  const meta = requestMeta()
  clearWebToken()
  beginSessionBoundary()
  await invoke(Logout, meta)
}

/** 完成首次安装，创建部署管理员和第一个工作区并建立登录会话。 */
export async function install(
  input: Omit<InstallWorkspaceInput, "locale" | "timeZone">,
) {
  return establishSession(
    await invoke((meta) =>
      InstallWorkspace(meta, {
        ...input,
        locale: resolveBrowserLanguage() as InstallWorkspaceInput["locale"],
        timeZone: resolveBrowserTimeZone(),
      }),
    ),
  )
}

/** 检测服务器并返回安装状态和部署形态。 */
export const probeServer = bind(ProbeServer)

/** 进入新的登录会话代次后验证并保存服务器地址。 */
export async function connectServer(serverURL: string) {
  beginSessionBoundary()
  await invoke((meta) => ConnectServer(meta, serverURL))
}
